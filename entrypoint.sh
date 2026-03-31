#!/bin/bash
eval $(aws-env) && printenv

ARGS="-database=$databaseName \
  -restoretargetdatabase=$restoreTargetDatabase \
  -region=$region \
  -securitygroup=$securitygroup \
  -restoredmasterpassword=$restoredmasterpassword \
  -dbparametergroup=$dbparametergroup \
  -dbtype=$type \
  -waitingDbTimeInMinutes=$waitingDbTimeInMinutes"

if [ -n "$targetRoleARN" ]; then
  ARGS="$ARGS -target-role-arn=$targetRoleARN"
fi

/app/rds-snapshot-restore $ARGS
