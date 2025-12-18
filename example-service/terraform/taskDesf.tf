variable "memory_mb" {
  type=number
}
variable "cpu_units" {
  type=number
}

//=[KAFKA]================================================================================
variable "kafkaBootstrapServer" {
  type = string
}
variable "KafkaGroupId" {
  type = string
}
variable "kafkaConsumerTopic" {
  type = string
}

//=[OPEN SEARCH]================================================================================
variable "openSearchURL" {
  type = string
}
variable "openSearchIndexPrefix" {
  type = string
}
//Service
variable "discardClaimsOlderThanDays" {
  type = string
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
    OPEN_SEARCH_USER = "open_search_user_name"
    OPEN_SEARCH_PASS = "open_search_password"
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
    { name = "KAFKA_TOPIC", value = var.kafkaConsumerTopic},
    { name = "KAFKA_URL", value = var.kafkaBootstrapServer},
    { name = "KAFKA_CONSUMER_GROUP", value = var.KafkaGroupId},
    { name = "OPEN_SEARCH_URL", value = var.openSearchURL},
    { name = "OPENSEARCH_INDEX_PREFIX", value = var.openSearchIndexPrefix},
    { name = "DISCARD_CLAIMS_OLDER_THAN_IN_DAYS", value = var.discardClaimsOlderThanDays},
  ]
  portMappings = [
    { containerPort = 8080 }
  ]
  secrets = [
    { name= "OPEN_SEARCH_USER", valueFrom="${aws_secretsmanager_secret.secrets.arn}:OPEN_SEARCH_USER::" },
    { name= "OPEN_SEARCH_PASS", valueFrom="${aws_secretsmanager_secret.secrets.arn}:OPEN_SEARCH_PASS::" },
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

resource "aws_iam_policy" "msk_iam_auth_policy" {
  name        = "${lower(var.project_name)}-msk-iam-auth-policy"
  description = "IAM policy for MSK IAM authentication"

  policy = jsonencode({
    Version = "2012-10-17",
    Statement = [
      {
        Effect = "Allow",
        Action = [
          "kafka-cluster:Connect",
          "kafka-cluster:DescribeCluster",
          "kafka-cluster:*Group*"
        ],
        Resource = [
          "*"
        ]
      },
      {
        Effect = "Allow",
        Action = [
          "kafka-cluster:*Topic",
          "kafka-cluster:WriteData",
          "kafka-cluster:ReadData"
        ],
        Resource = [
          "arn:aws:kafka:*:*:topic/*/*/${var.kafkaConsumerTopic}*"
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "task_role_msk_attach" {
  role       = module.testTaskDef.task_role_name
  policy_arn = aws_iam_policy.msk_iam_auth_policy.arn
}
