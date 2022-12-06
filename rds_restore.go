package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/rds"
)

func parseTime(layout, value string) *time.Time {
	t, err := time.Parse(layout, value)
	if err != nil {
		panic(err)
	}
	return &t
}

func main() {
	var (
		databaseName           string
		restoreTargetDatabase  string
		region                 string
		securitygroup          string
		restoredmasterpassword string
		dbparametergroup       string
		dbType                 string
		waitingDbTimeInMinutes int
		err                    error
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

	db := databaseName
	dbr := restoreTargetDatabase

	printInfo("Deleting previous restored DB instance")
	deleteresult, err := deleteRestoredDBInstance(dbr, region)
	if err == nil {
		printInfo(deleteresult)
		printInfo("Waiting for instance to be deleted...")
		err = waitDBInstanceDeleted(dbr, region)
	}
	if err != nil {
		printError("Previous restored DB instance doesn't exist")
	}

	printInfo("Creating restored DB instance")
	restoreresult, err := restoreDBInstanceToPointInTime(db, dbr, region, dbType, securitygroup, dbparametergroup)
	if err != nil {
		printError(err)
		return
	}
	printInfo(restoreresult)

	printInfo("Waiting for instance to become available...")
	err = waitDBInstanceAvailable(dbr, region, waitingDbTimeInMinutes)
	if err != nil {
		printError(err)
		return
	}

	printInfo("Changing restored database parameters...")
	err = changeDBInstance(dbr, region, restoredmasterpassword)
	if err != nil {
		printError(err)
	}

	printInfo("Waiting for instance to become available...")
	err = waitDBInstanceAvailable(dbr, region, waitingDbTimeInMinutes)
	if err != nil {
		printError(err)
		return
	}

	printInfo("Restarting restored database...")
	err = restartDBInstance(dbr, region)
	if err != nil {
		printError(err)
	}

	printInfo("Waiting for instance to become available...")
	err = waitDBInstanceAvailable(dbr, region, waitingDbTimeInMinutes)
	if err != nil {
		printError(err)
		return
	}

	//	printInfo("cleaning up tests")
	//	deleteresultafter, err := deleteRestoredDBInstance(dbr, region)
	//	if err != nil {
	//		printError(err)
	//	}
	//	printInfo(deleteresultafter)
}

//Wait for old db to be deleted
func waitDBInstanceDeleted(dbr string, region string) error {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	input := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbr),
	}

	err := svc.WaitUntilDBInstanceDeleted(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			printError(aerr.Error())
		} else {
			printError(err.Error())
		}
		return err
	}
	return nil
}

//Wait for db to become available
func waitDBInstanceAvailable(dbr string, region string, waitingDbTimeInMinutes int) error {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	input := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbr),
	}

	err := svc.WaitUntilDBInstanceAvailableWithContext(
		aws.BackgroundContext(),
		input,
		request.WithWaiterDelay(request.ConstantWaiterDelay(30*time.Second)), // check every 30 seconds
		request.WithWaiterMaxAttempts(waitingDbTimeInMinutes*2))              // waiting time in minutes (depends on the previous line)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			printError(aerr.Error())
		} else {
			printError(err.Error())
		}
		return err
	}
	return nil
}

//Removes Old restored database
func deleteRestoredDBInstance(dbr string, region string) (bool, error) {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	deleteinput := &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbr),
		SkipFinalSnapshot:    aws.Bool(true),
	}

	_, err := svc.DeleteDBInstance(deleteinput)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			printError(aerr.Error())
		} else {
			printError(err.Error())
		}
		return false, err
	}
	return true, nil
}

//Restores database
func restoreDBInstanceToPointInTime(db string, dbr string, region string, dbType string, securitygroup string, dbparametergroup string) (bool, error) {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	now := time.Now()
	nowSubstractTenMinutes := now.Add(-10 * time.Minute)
	input := &rds.RestoreDBInstanceToPointInTimeInput{
		RestoreTime:                aws.Time(nowSubstractTenMinutes),
		SourceDBInstanceIdentifier: aws.String(db),
		TargetDBInstanceIdentifier: aws.String(dbr),
		PubliclyAccessible:         aws.Bool(true),
		DBInstanceClass:            aws.String(dbType),
		MultiAZ:                    aws.Bool(false),
		VpcSecurityGroupIds:        aws.StringSlice([]string{securitygroup}),
		DBParameterGroupName:       aws.String(dbparametergroup),
		AutoMinorVersionUpgrade:    aws.Bool(false),
	}

	_, err := svc.RestoreDBInstanceToPointInTime(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			printError(aerr.Error())
		} else {
			printError(err.Error())
		}
		return false, err
	}
	return true, nil
}

//Change rds instance type (change master user password and disable backups)
func changeDBInstance(dbr string, region string, restoredmasterpassword string) error {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	input := &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier:  aws.String(dbr),
		ApplyImmediately:      aws.Bool(true),
		BackupRetentionPeriod: aws.Int64(0), // prevent backups
		MasterUserPassword:    aws.String(restoredmasterpassword),
	}

	_, err := svc.ModifyDBInstance(input)
	time.Sleep(15 * time.Second)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			printError(aerr.Error())
		} else {
			printError(err.Error())
		}
		return err
	}
	return nil
}

func restartDBInstance(dbr string, region string) error {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	input := &rds.RebootDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbr),
		ForceFailover:        aws.Bool(false),
	}
	_, err := svc.RebootDBInstance(input)
	time.Sleep(15 * time.Second)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			printError(aerr.Error())
		} else {
			printError(err.Error())
		}
		return err
	}
	return nil
}

func printError(a ...interface{}) {
	fmt.Println("ERROR:", a)
}

func printInfo(a ...interface{}) {
	fmt.Println("INFO:", a)
}
