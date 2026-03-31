# RDS Snapshot Restore

Automates the AWS RDS restore process using point-in-time recovery (same account) or snapshot sharing (cross-account). It restores a source database to a new instance, applies configuration changes, and leaves it ready to use — without touching production.

## How it works

**Same-account restore** (default) uses `RestoreDBInstanceToPointInTime` to create a copy of the source DB from 10 minutes ago. This is the original behavior — no new flags are required and existing usage is unaffected.

**Cross-account restore** is opt-in via the `-target-role-arn` flag. When provided, the tool creates a manual snapshot, shares it with the target account, assumes the given IAM role, and restores from the snapshot there.

---

## Usage

### Same account

```bash
AWS_PROFILE=yourprofile go run rds_restore.go \
  -database=mydb \
  -restoretargetdatabase=mydb-restore \
  -region=eu-west-1 \
  -securitygroup=sg-123456 \
  -restoredmasterpassword=yourpassword \
  -dbparametergroup=your-param-group \
  -dbtype=db.t3.medium
```

### Cross-account

```bash
AWS_PROFILE=source-account-profile go run rds_restore.go \
  -database=mydb \
  -restoretargetdatabase=mydb-restore \
  -region=eu-west-1 \
  -securitygroup=sg-789012 \
  -restoredmasterpassword=yourpassword \
  -dbparametergroup=your-param-group \
  -dbtype=db.t3.medium \
  -target-role-arn=arn:aws:iam::123456789012:role/RDSRestoreRole
```

The credentials from `AWS_PROFILE` are used for the source account. The tool assumes `target-role-arn` to perform all operations in the target account.

---

## Docker

### Build

```bash
docker build -t rds-snapshot-restore .
```

### Run

The container uses the AWS credentials available in its environment. Prefer attaching an IAM role (ECS task role, EC2 instance profile) over passing static credentials — if you must supply credentials manually, use short-lived session tokens rather than long-lived access keys.

Parameters are passed as environment variables — `entrypoint.sh` maps them to the corresponding flags automatically.

**Same account**

```bash
docker run --rm \
  -e databaseName=mydb \
  -e restoreTargetDatabase=mydb-restore \
  -e region=eu-west-1 \
  -e securitygroup=sg-123456 \
  -e restoredmasterpassword=yourpassword \
  -e dbparametergroup=your-param-group \
  -e type=db.t3.medium \
  rds-snapshot-restore
```

**Cross-account** — add `targetRoleARN`, no other changes needed:

```bash
docker run --rm \
  -e databaseName=mydb \
  -e restoreTargetDatabase=mydb-restore \
  -e region=eu-west-1 \
  -e securitygroup=sg-789012 \
  -e restoredmasterpassword=yourpassword \
  -e dbparametergroup=your-param-group \
  -e type=db.t3.medium \
  -e targetRoleARN=arn:aws:iam::123456789012:role/RDSRestoreRole \
  rds-snapshot-restore
```

### Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `databaseName` | Yes | Source DB instance identifier |
| `restoreTargetDatabase` | Yes | Name for the restored DB instance |
| `region` | Yes | AWS region |
| `securitygroup` | Yes | Security group ID to attach to the restored instance |
| `restoredmasterpassword` | Yes | Master password to set on the restored instance |
| `dbparametergroup` | Yes | Parameter group name for the restored instance |
| `type` | Yes | DB instance class (e.g. `db.t3.medium`) |
| `targetRoleARN` | No | IAM role ARN in the target account for cross-account restore |
| `waitingDbTimeInMinutes` | No | Max minutes to wait for instance availability (default: 35) |

---

## All flags

| Flag | Required | Description |
|------|----------|-------------|
| `-database` | Yes | Source DB instance identifier |
| `-restoretargetdatabase` | Yes | Name for the restored DB instance |
| `-region` | Yes | AWS region |
| `-securitygroup` | Yes | Security group ID to attach to the restored instance |
| `-restoredmasterpassword` | Yes | Master password to set on the restored instance |
| `-dbparametergroup` | Yes | Parameter group name for the restored instance |
| `-dbtype` | Yes | DB instance class (e.g. `db.t3.medium`) |
| `-target-role-arn` | No | IAM role ARN in the target account for cross-account restore |
| `-waitingDbTimeInMinutes` | No | Max minutes to wait for instance availability (default: 35) |

---

## IAM permissions

### Source account (same-account restore)

```json
{
  "Effect": "Allow",
  "Action": [
    "rds:Describe*",
    "rds:List*",
    "rds:DeleteDBInstance",
    "rds:RestoreDBInstanceToPointInTime",
    "rds:ModifyDBInstance",
    "rds:RebootDBInstance"
  ],
  "Resource": "*"
}
```

### Source account (cross-account restore)

```json
{
  "Effect": "Allow",
  "Action": [
    "rds:Describe*",
    "rds:List*",
    "rds:CreateDBSnapshot",
    "rds:ModifyDBSnapshotAttribute",
    "sts:AssumeRole"
  ],
  "Resource": "*"
}
```

### Target account role (`RDSRestoreRole`)

```json
{
  "Effect": "Allow",
  "Action": [
    "rds:Describe*",
    "rds:List*",
    "rds:DeleteDBInstance",
    "rds:RestoreDBInstanceFromDBSnapshot",
    "rds:ModifyDBInstance",
    "rds:RebootDBInstance"
  ],
  "Resource": "*"
}
```

The trust policy on this role must allow the source account to assume it:

```json
{
  "Effect": "Allow",
  "Principal": {
    "AWS": "arn:aws:iam::SOURCE_ACCOUNT_ID:root"
  },
  "Action": "sts:AssumeRole"
}
```

---

## Remarks

- The restored instance is created with `PubliclyAccessible: true`, `MultiAZ: false`, and automated backups disabled. Adjust these in the source if needed.
- The tool will **delete** any existing instance named `-restoretargetdatabase` before restoring. Make sure deletion protection is **off** on that instance.
- For cross-account restores, automated snapshots cannot be shared — the tool creates a manual snapshot automatically.
- Provide a security group that exists in the correct VPC for the target region/account.
