variable "memory_mb" {
  type    = number
  default = 512
}
variable "cpu_units" {
  type    = number
  default = 256
}

//=[DATABASE CONFIGURATION]================================================================================
variable "snowflake_account" {
  type        = string
  description = "Snowflake account identifier (e.g., dwwwkin-east)"
}
variable "snowflake_database" {
  type        = string
  description = "Snowflake database name (e.g., CPE_DEV)"
}
variable "snowflake_schema" {
  type        = string
  description = "Snowflake schema name (e.g., DATA)"
}
variable "snowflake_sql" {
  type        = string
  description = "SQL query to load data from Snowflake"
}
variable "postgres_sql" {
  type        = string
  description = "SQL query to load data from PostgreSQL"
}

//=[COMPARISON SETTINGS]================================================================================
variable "comparison_interval" {
  type        = string
  default     = "5m"
  description = "Interval between cache comparisons (Go duration format)"
}
variable "cache_check_interval" {
  type        = string
  default     = "60s"
  description = "Interval for cache refresh checks (Go duration format)"
}
variable "max_detailed_mismatches" {
  type        = number
  default     = 100
  description = "Maximum number of detailed mismatches to log"
}
variable "monitored_tables" {
  type        = string
  default     = "API_KEYS"
  description = "Comma-separated list of tables to monitor for cache invalidation"
}
variable "key_field" {
  type        = string
  default     = "Key"
  description = "Field name used as the cache key"
}
variable "log_level" {
  type        = string
  default     = "info"
  description = "Log level (debug, info, warn, error)"
}

//=[EXISTING SECRETS REFERENCES]================================================================================
// Reference existing secrets instead of creating new ones
data "aws_secretsmanager_secret" "snowflake_secret" {
  name = "SNOWFLAKE_CONNECTION_BATCH_WR"
}

data "aws_secretsmanager_secret" "postgres_secret" {
  name = "DB_URL_RULE_DATA_READ"
}

variable "image_full" {}

resource "aws_cloudwatch_log_group" "logGroup" {
  name = lower(var.project_name)
}

module "main-Container" {
  source        = "git::https://github.com/transactrx/terrform-modules.git//modules/container-definition?ref=master"
  containerName = "main"
  cpu           = var.cpu_units - 2
  imageURL      = "$$MAIN_IMAGE$$"
  memory        = var.memory_mb - 1
  logGroup      = aws_cloudwatch_log_group.logGroup.name
  envVariables = [
    # Snowflake connection parameters (non-secret) - warehouse/role use user defaults
    { name = "SNOWFLAKE_ACCOUNT", value = var.snowflake_account },
    { name = "SNOWFLAKE_DATABASE", value = var.snowflake_database },
    { name = "SNOWFLAKE_SCHEMA", value = var.snowflake_schema },
    # SQL queries
    { name = "SNOWFLAKE_SQL", value = var.snowflake_sql },
    { name = "POSTGRES_SQL", value = var.postgres_sql },
    # Comparison settings
    { name = "COMPARISON_INTERVAL", value = var.comparison_interval },
    { name = "CACHE_CHECK_INTERVAL", value = var.cache_check_interval },
    { name = "MAX_DETAILED_MISMATCHES", value = tostring(var.max_detailed_mismatches) },
    { name = "MONITORED_TABLES", value = var.monitored_tables },
    { name = "KEY_FIELD", value = var.key_field },
    { name = "LOG_LEVEL", value = var.log_level },
  ]
  portMappings = []
  secrets = [
    # Snowflake credentials from existing secret SNOWFLAKE_CONNECTION_BATCH_WR
    { name = "SNOWFLAKE_USER", valueFrom = "${data.aws_secretsmanager_secret.snowflake_secret.arn}:username::" },
    { name = "SNOWFLAKE_PRIVATE_KEY", valueFrom = "${data.aws_secretsmanager_secret.snowflake_secret.arn}:private_key::" },
    # PostgreSQL connection string from existing secret DB_URL_RULE_DATA_READ
    { name = "POSTGRES_DSN", valueFrom = "${data.aws_secretsmanager_secret.postgres_secret.arn}:DB_URL::" },
  ]
}

module "testTaskDef" {
  source        = "git::https://github.com/transactrx/terrform-modules.git//modules/task-definition?ref=master"
  CPU           = var.cpu_units
  ContainerList = [module.main-Container.ContainerDefObject]
  Memory        = var.memory_mb
  taskDefFamily = lower(var.project_name)
  mainImageURL  = var.image_full
}

output "taskDef" {
  value = module.testTaskDef
}
