package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
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
		err                    error
	)
	flag.StringVar(&databaseName, "database", "", "The source database")
	flag.StringVar(&restoreTargetDatabase, "restoretargetdatabase", "", "The target name of the restored database")
	flag.StringVar(&region, "region", "", "The region of the aws resources")
	flag.StringVar(&securitygroup, "securitygroup", "", "The securitygroup of the restored RDS")
	flag.StringVar(&restoredmasterpassword, "restoredmasterpassword", "", "The desired password of the restored RDS")
	flag.StringVar(&dbparametergroup, "dbparametergroup", "", "The desired db parametergroup of the restored RDS")

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

	t := time.Now()
	db := databaseName
	dbr := restoreTargetDatabase

	fmt.Println("Deleting previous restored DB instance")
	deleteresult, err := deleteRestoredDBInstance(dbr, region)
	if err == nil {
		fmt.Println(deleteresult)
		fmt.Println("Waiting for instance to be deleted...")
		err = waitDBInstanceDeleted(dbr, region)
	}
	if err != nil {
		fmt.Println("Previous restored DB instance doesn't exist")
	}

	fmt.Println("Creating restored DB instance")
	restoreresult, err := restoreDBInstanceToPointInTime(t, db, dbr, region)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(restoreresult)

	fmt.Println("Waiting for instance to become available...")
	err = waitDBInstanceAvailable(dbr, region)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Changing restored database parameters...")
	err = changeDBInstance(dbr, region, securitygroup, restoredmasterpassword, dbparametergroup)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("Waiting for instance to become available...")
	err = waitDBInstanceAvailable(dbr, region)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Restarting restored database...")
	err = restartDBInstance(dbr, region)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("Waiting for instance to become available...")
	err = waitDBInstanceAvailable(dbr, region)
	if err != nil {
		fmt.Println(err)
		return
	}

	//	fmt.Println("cleaning up tests")
	//	deleteresultafter, err := deleteRestoredDBInstance(dbr, region)
	//	if err != nil {
	//		fmt.Println(err)
	//	}
	//	fmt.Println(deleteresultafter)
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
			fmt.Println(aerr.Error())
		} else {
			fmt.Println(err.Error())
		}
		return err
	}
	return nil
}

//Wait for db to become available
func waitDBInstanceAvailable(dbr string, region string) error {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	input := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbr),
	}

	err := svc.WaitUntilDBInstanceAvailable(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			fmt.Println(aerr.Error())
		} else {
			fmt.Println(err.Error())
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
			fmt.Println(aerr.Error())
		} else {
			fmt.Println(err.Error())
		}
		return false, err
	}
	return true, nil
}

//Restores database
func restoreDBInstanceToPointInTime(t time.Time, db string, dbr string, region string) (bool, error) {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	now := time.Now()
	nowSubstractTenMinutes := now.Add(-10 * time.Minute)
	input := &rds.RestoreDBInstanceToPointInTimeInput{
		RestoreTime:                aws.Time(nowSubstractTenMinutes),
		SourceDBInstanceIdentifier: aws.String(db),
		TargetDBInstanceIdentifier: aws.String(dbr),
	}

	_, err := svc.RestoreDBInstanceToPointInTime(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			fmt.Println(aerr.Error())
		} else {
			fmt.Println(err.Error())
		}
		return false, err
	}
	return true, nil
}

//Change rds instance type
func changeDBInstance(dbr string, region string, securitygroup string, restoredmasterpassword string, dbparametergroup string) error {
	svc := rds.New(session.New(), &aws.Config{Region: aws.String(region)})
	input := &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier:  aws.String(dbr),
		ApplyImmediately:      aws.Bool(true),
		PubliclyAccessible:    aws.Bool(true),
		BackupRetentionPeriod: aws.Int64(0),
		DBInstanceClass:       aws.String("db.t3.micro"),
		MasterUserPassword:    aws.String(restoredmasterpassword),
		MultiAZ:               aws.Bool(false),
		VpcSecurityGroupIds:   aws.StringSlice([]string{securitygroup}),
		DBParameterGroupName:  aws.String(dbparametergroup),
	}

	_, err := svc.ModifyDBInstance(input)
	time.Sleep(15 * time.Second)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			fmt.Println(aerr.Error())
		} else {
			fmt.Println(err.Error())
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
			fmt.Println(aerr.Error())
		} else {
			fmt.Println(err.Error())
		}
		return err
	}
	return nil
}
