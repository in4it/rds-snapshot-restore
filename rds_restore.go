package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		databaseName           string
		restoreTargetDatabase  string
		region                 string
		securitygroup          string
		restoredmasterpassword string
		dbparametergroup       string
		dbType                 string
		waitingDbTimeInMinutes int
		targetRoleARN          string
	)
	const defaultWaitingDbTimeInMinutes = 35 // the default RDS backup duration is 30 minutes. Hence, we need to wait a bit more than 30 minutes
	flag.StringVar(&databaseName, "database", "", "The source database")
	flag.StringVar(&restoreTargetDatabase, "restoretargetdatabase", "", "The target name of the restored database")
	flag.StringVar(&region, "region", "", "The region of the aws resources")
	flag.StringVar(&securitygroup, "securitygroup", "", "The securitygroup of the restored RDS")
	flag.StringVar(&restoredmasterpassword, "restoredmasterpassword", "", "The desired password of the restored RDS")
	flag.StringVar(&dbparametergroup, "dbparametergroup", "", "The desired db parametergroup of the restored RDS")
	flag.StringVar(&dbType, "dbtype", "", "The desired db type of the restored RDS")
	flag.IntVar(&waitingDbTimeInMinutes, "waitingDbTimeInMinutes", defaultWaitingDbTimeInMinutes, "The desired waiting time in minutes for the restored RDS. This is required to apply the changes to the restored RDS")
	flag.StringVar(&targetRoleARN, "target-role-arn", "", "IAM role ARN in the target account to assume for cross-account restore (e.g. arn:aws:iam::123456789012:role/RDSRestoreRole)")

	flag.Parse()

	if databaseName != "" {
		os.Setenv("databaseName", databaseName)
	}
	if restoreTargetDatabase != "" {
		os.Setenv("restoreTargetDatabase", restoreTargetDatabase)
	}
	if region != "" {
		os.Setenv("region", region)
	}
	if securitygroup != "" {
		os.Setenv("securitygroup", securitygroup)
	}
	if restoredmasterpassword != "" {
		os.Setenv("restoredmasterpassword", restoredmasterpassword)
	}
	if dbparametergroup != "" {
		os.Setenv("dbparametergroup", dbparametergroup)
	}
	if waitingDbTimeInMinutes != defaultWaitingDbTimeInMinutes {
		os.Setenv("waitingDbTimeInMinutes", string(rune(waitingDbTimeInMinutes)))
	}

	ctx := context.Background()

	sourceCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		printError(err)
		return 1
	}

	if targetRoleARN != "" {
		return runCrossAccount(ctx, databaseName, restoreTargetDatabase, dbType, securitygroup, dbparametergroup, restoredmasterpassword, targetRoleARN, waitingDbTimeInMinutes, sourceCfg)
	}

	return runSameAccount(ctx, databaseName, restoreTargetDatabase, dbType, securitygroup, dbparametergroup, restoredmasterpassword, waitingDbTimeInMinutes, sourceCfg)
}

func runSameAccount(ctx context.Context, db, dbr, dbType, securitygroup, dbparametergroup, restoredmasterpassword string, waitingDbTimeInMinutes int, cfg aws.Config) int {
	printInfo("Deleting previous restored DB instance")
	deleteresult, err := deleteRestoredDBInstance(ctx, dbr, cfg)
	if err == nil {
		printInfo(deleteresult)
		printInfo("Waiting for instance to be deleted...")
		err = waitDBInstanceDeleted(ctx, dbr, cfg)
	}
	if err != nil {
		printError("Previous restored DB instance doesn't exist")
	}

	printInfo("Creating restored DB instance")
	restoreresult, err := restoreDBInstanceToPointInTime(ctx, db, dbr, dbType, securitygroup, dbparametergroup, cfg)
	if err != nil {
		printError(err)
		return 1
	}
	printInfo(restoreresult)

	printInfo("Waiting for instance to become available...")
	if err = waitDBInstanceAvailable(ctx, dbr, waitingDbTimeInMinutes, cfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Changing restored database parameters...")
	if err = changeDBInstance(ctx, dbr, restoredmasterpassword, cfg); err != nil {
		printError(err)
	}

	printInfo("Waiting for instance to become available...")
	if err = waitDBInstanceAvailable(ctx, dbr, waitingDbTimeInMinutes, cfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Restarting restored database...")
	if err = restartDBInstance(ctx, dbr, cfg); err != nil {
		printError(err)
	}

	printInfo("Waiting for instance to become available...")
	if err = waitDBInstanceAvailable(ctx, dbr, waitingDbTimeInMinutes, cfg); err != nil {
		printError(err)
		return 1
	}

	return 0
}

// runCrossAccount creates a manual snapshot in the source account, shares it with the
// target account, assumes the target role, and restores the DB there.
func runCrossAccount(ctx context.Context, db, dbr, dbType, securitygroup, dbparametergroup, restoredmasterpassword, targetRoleARN string, waitingDbTimeInMinutes int, sourceCfg aws.Config) int {
	targetAccountID, err := accountIDFromRoleARN(targetRoleARN)
	if err != nil {
		printError(err)
		return 1
	}

	sourceAccountID, err := getCallerAccountID(ctx, sourceCfg)
	if err != nil {
		printError(err)
		return 1
	}

	snapshotID := fmt.Sprintf("%s-cross-account-%d", db, time.Now().Unix())

	printInfo("Creating snapshot in source account:", snapshotID)
	if err = createDBSnapshot(ctx, db, snapshotID, sourceCfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Waiting for snapshot to become available...")
	if err = waitDBSnapshotAvailable(ctx, snapshotID, sourceCfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Sharing snapshot with target account:", targetAccountID)
	if err = shareDBSnapshot(ctx, snapshotID, targetAccountID, sourceCfg); err != nil {
		printError(err)
		return 1
	}

	snapshotARN := fmt.Sprintf("arn:aws:rds:%s:%s:snapshot:%s", sourceCfg.Region, sourceAccountID, snapshotID)

	// Build a config for the target account by assuming the provided role
	stsClient := sts.NewFromConfig(sourceCfg)
	targetCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(sourceCfg.Region),
		config.WithCredentialsProvider(aws.NewCredentialsCache(
			stscreds.NewAssumeRoleProvider(stsClient, targetRoleARN),
		)),
	)
	if err != nil {
		printError(err)
		return 1
	}

	printInfo("Deleting previous restored DB instance in target account")
	deleteresult, err := deleteRestoredDBInstance(ctx, dbr, targetCfg)
	if err == nil {
		printInfo(deleteresult)
		printInfo("Waiting for instance to be deleted...")
		err = waitDBInstanceDeleted(ctx, dbr, targetCfg)
	}
	if err != nil {
		printError("Previous restored DB instance doesn't exist")
	}

	printInfo("Restoring DB from shared snapshot in target account")
	if err = restoreDBInstanceFromSnapshot(ctx, snapshotARN, dbr, dbType, securitygroup, dbparametergroup, targetCfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Waiting for instance to become available...")
	if err = waitDBInstanceAvailable(ctx, dbr, waitingDbTimeInMinutes, targetCfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Changing restored database parameters...")
	if err = changeDBInstance(ctx, dbr, restoredmasterpassword, targetCfg); err != nil {
		printError(err)
	}

	printInfo("Waiting for instance to become available...")
	if err = waitDBInstanceAvailable(ctx, dbr, waitingDbTimeInMinutes, targetCfg); err != nil {
		printError(err)
		return 1
	}

	printInfo("Restarting restored database...")
	if err = restartDBInstance(ctx, dbr, targetCfg); err != nil {
		printError(err)
	}

	printInfo("Waiting for instance to become available...")
	if err = waitDBInstanceAvailable(ctx, dbr, waitingDbTimeInMinutes, targetCfg); err != nil {
		printError(err)
		return 1
	}

	return 0
}

func accountIDFromRoleARN(roleARN string) (string, error) {
	// arn:aws:iam::123456789012:role/RoleName
	parts := strings.Split(roleARN, ":")
	if len(parts) < 5 {
		return "", fmt.Errorf("invalid role ARN: %s", roleARN)
	}
	return parts[4], nil
}

func getCallerAccountID(ctx context.Context, cfg aws.Config) (string, error) {
	stsClient := sts.NewFromConfig(cfg)
	result, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get caller identity: %w", err)
	}
	return aws.ToString(result.Account), nil
}

func createDBSnapshot(ctx context.Context, db, snapshotID string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	_, err := svc.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
		DBInstanceIdentifier: aws.String(db),
		DBSnapshotIdentifier: aws.String(snapshotID),
	})
	return wrapRDSError(err)
}

func waitDBSnapshotAvailable(ctx context.Context, snapshotID string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	waiter := rds.NewDBSnapshotAvailableWaiter(svc)
	err := waiter.Wait(ctx, &rds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String(snapshotID),
	}, 60*time.Minute)
	return wrapRDSError(err)
}

func shareDBSnapshot(ctx context.Context, snapshotID, targetAccountID string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	_, err := svc.ModifyDBSnapshotAttribute(ctx, &rds.ModifyDBSnapshotAttributeInput{
		DBSnapshotIdentifier: aws.String(snapshotID),
		AttributeName:        aws.String("restore"),
		ValuesToAdd:          []string{targetAccountID},
	})
	return wrapRDSError(err)
}

func restoreDBInstanceFromSnapshot(ctx context.Context, snapshotARN, dbr, dbType, securitygroup, dbparametergroup string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	_, err := svc.RestoreDBInstanceFromDBSnapshot(ctx, &rds.RestoreDBInstanceFromDBSnapshotInput{
		DBSnapshotIdentifier:    aws.String(snapshotARN),
		DBInstanceIdentifier:    aws.String(dbr),
		PubliclyAccessible:      aws.Bool(true),
		DBInstanceClass:         aws.String(dbType),
		MultiAZ:                 aws.Bool(false),
		VpcSecurityGroupIds:     []string{securitygroup},
		DBParameterGroupName:    aws.String(dbparametergroup),
		AutoMinorVersionUpgrade: aws.Bool(false),
	})
	return wrapRDSError(err)
}

func waitDBInstanceDeleted(ctx context.Context, dbr string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	waiter := rds.NewDBInstanceDeletedWaiter(svc)
	err := waiter.Wait(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbr),
	}, 60*time.Minute)
	return wrapRDSError(err)
}

func waitDBInstanceAvailable(ctx context.Context, dbr string, waitingDbTimeInMinutes int, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	waiter := rds.NewDBInstanceAvailableWaiter(svc, func(o *rds.DBInstanceAvailableWaiterOptions) {
		o.MinDelay = 30 * time.Second
		o.MaxDelay = 30 * time.Second
	})
	err := waiter.Wait(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbr),
	}, time.Duration(waitingDbTimeInMinutes)*time.Minute)
	return wrapRDSError(err)
}

func deleteRestoredDBInstance(ctx context.Context, dbr string, cfg aws.Config) (bool, error) {
	svc := rds.NewFromConfig(cfg)
	_, err := svc.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbr),
		SkipFinalSnapshot:    aws.Bool(true),
	})
	if err != nil {
		return false, wrapRDSError(err)
	}
	return true, nil
}

func restoreDBInstanceToPointInTime(ctx context.Context, db, dbr, dbType, securitygroup, dbparametergroup string, cfg aws.Config) (bool, error) {
	svc := rds.NewFromConfig(cfg)
	nowSubtractTenMinutes := time.Now().Add(-10 * time.Minute)
	_, err := svc.RestoreDBInstanceToPointInTime(ctx, &rds.RestoreDBInstanceToPointInTimeInput{
		RestoreTime:                aws.Time(nowSubtractTenMinutes),
		SourceDBInstanceIdentifier: aws.String(db),
		TargetDBInstanceIdentifier: aws.String(dbr),
		PubliclyAccessible:         aws.Bool(true),
		DBInstanceClass:            aws.String(dbType),
		MultiAZ:                    aws.Bool(false),
		VpcSecurityGroupIds:        []string{securitygroup},
		DBParameterGroupName:       aws.String(dbparametergroup),
		AutoMinorVersionUpgrade:    aws.Bool(false),
	})
	if err != nil {
		return false, wrapRDSError(err)
	}
	return true, nil
}

func changeDBInstance(ctx context.Context, dbr, restoredmasterpassword string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	_, err := svc.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier:  aws.String(dbr),
		ApplyImmediately:      aws.Bool(true),
		BackupRetentionPeriod: aws.Int32(0), // prevent backups
		MasterUserPassword:    aws.String(restoredmasterpassword),
	})
	time.Sleep(15 * time.Second)
	return wrapRDSError(err)
}

func restartDBInstance(ctx context.Context, dbr string, cfg aws.Config) error {
	svc := rds.NewFromConfig(cfg)
	_, err := svc.RebootDBInstance(ctx, &rds.RebootDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbr),
		ForceFailover:        aws.Bool(false),
	})
	time.Sleep(15 * time.Second)
	return wrapRDSError(err)
}

// wrapRDSError extracts a meaningful message from smithy/RDS errors.
func wrapRDSError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%s: %s", apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
	// Unwrap ResourceNotFoundFault so callers can detect missing instances
	var notFound *rdstypes.DBInstanceNotFoundFault
	if errors.As(err, &notFound) {
		return notFound
	}
	return err
}

func printError(a ...interface{}) {
	fmt.Println("ERROR:", a)
}

func printInfo(a ...interface{}) {
	fmt.Println("INFO:", a)
}
