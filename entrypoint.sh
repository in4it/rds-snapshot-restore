#!/bin/bash
eval $(aws-env) && printenv && /app/rds-snapshot-restore -database=$databaseName -restoretargetdatabase=$restoreTargetDatabase -region=$region -securitygroup=$securitygroup -restoredmasterpassword=$restoredmasterpassword -dbparametergroup=$dbparametergroup -dbtype=$type
