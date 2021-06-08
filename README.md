# RDS snapshot restore

The goal of this tool is to automate the restore/creation/RDS modifications process. This was developed to give the client access to recent production data(start of script -10 min nowSubstractTenMinutes) without impacting production and leveraging the snapshots and AWS RDS point in time restore capabilities. 

#Remarks

* Make sure the deletenion protection is on on the databases with the `-database=` parameter
* provide a valid security group id, if the database is marked publicly make sure the SG is created in the correct VPC
* this script wil delete the previously created database `-restoretargetdatabase=`

*Example to run command:*
```
AWS_PROFILE=yourprofile go run rds_restore.go -database=yourdatabase -restoretargetdatabase=yourdatabase-restore -region=eu-west-1 -securitygroup=sg-123456 -restoredmasterpassword=yourpassword -dbparametergroup=rds-restore
```

# IAM
```
        "rds:Describe*",
        "rds:List*",
        "rds:CreateDBInstance",
        "rds:DeleteDBInstance",
        "rds:RestoreDBInstanceToPointInTime",
        "rds:ModifyDBInstance",
        "rds:ApplyPendingMaintenanceAction",
        "rds:RebootDBInstance"```
