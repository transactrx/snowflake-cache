variable "memory_mb" {
  type    = number
  default = 512
}
variable "cpu_units" {
  type    = number
  default = 256
}

//=[DATABASE CONFIGURATION]================================================================================
variable "snowflake_database_schema" {
  type        = string
  description = "Snowflake database.schema (e.g., MY_DATABASE.MY_SCHEMA)"
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

resource "aws_secretsmanager_secret" "secrets" {
  name = "${lower(var.project_name)}/secret"
}

resource "aws_secretsmanager_secret_version" "secret_defaults" {
  secret_id = aws_secretsmanager_secret.secrets.id
  lifecycle {
    ignore_changes = [secret_string]
  }

  secret_string = jsonencode({
    SNOWFLAKE_DSN = "snowflake_dsn_placeholder"
    POSTGRES_DSN  = "postgres_dsn_placeholder"
  })
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
    { name = "SNOWFLAKE_DATABASE_SCHEMA", value = var.snowflake_database_schema },
    { name = "SNOWFLAKE_SQL", value = var.snowflake_sql },
    { name = "POSTGRES_SQL", value = var.postgres_sql },
    { name = "COMPARISON_INTERVAL", value = var.comparison_interval },
    { name = "CACHE_CHECK_INTERVAL", value = var.cache_check_interval },
    { name = "MAX_DETAILED_MISMATCHES", value = tostring(var.max_detailed_mismatches) },
    { name = "MONITORED_TABLES", value = var.monitored_tables },
    { name = "KEY_FIELD", value = var.key_field },
    { name = "LOG_LEVEL", value = var.log_level },
  ]
  portMappings = []
  secrets = [
    { name = "SNOWFLAKE_DSN", valueFrom = "${aws_secretsmanager_secret.secrets.arn}:SNOWFLAKE_DSN::" },
    { name = "POSTGRES_DSN", valueFrom = "${aws_secretsmanager_secret.secrets.arn}:POSTGRES_DSN::" },
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
