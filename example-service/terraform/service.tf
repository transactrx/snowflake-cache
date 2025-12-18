data "aws_ssm_parameter" "private-subnet-ids" {
  name = "private-subnet-ids"
}
data "aws_ssm_parameter" "vpc_id" {
  name = "vpc-id"
}

variable "desired_count" {
  type        = number
  default     = 1
  description = "Desired number of service instances"
}
variable "ecs_cluster_name" {
  type        = string
  description = "Name of the ECS cluster"
}

# Auto-scaler config is optional for this background service
# since it doesn't need to scale based on traffic
variable "auto_scaler_config" {
  type = object({
    max_capacity               = number
    min_capacity               = number
    enable_cpu_scaling         = bool
    cpu_scale_out_target_value = number
    cpu_scale_in_target_value  = number
    cpu_scale_in_cooldown      = number
    cpu_scale_out_cooldown     = number
  })
  default = {
    max_capacity               = 1
    min_capacity               = 1
    enable_cpu_scaling         = false
    cpu_scale_out_target_value = 70
    cpu_scale_in_target_value  = 30
    cpu_scale_in_cooldown      = 300
    cpu_scale_out_cooldown     = 300
  }
}

module "example-cache-service" {
  source                         = "git::https://github.com/transactrx/terrform-modules.git//modules/ecs-service?ref=master"
  clusterName                    = var.ecs_cluster_name
  networkLoadBalancerAttachments = []
  serviceName                    = lower(var.project_name)
  vpc_id                         = data.aws_ssm_parameter.vpc_id.value
  taskDefinitionFull             = module.testTaskDef.task_definition_full_path
  subNets                        = jsondecode(data.aws_ssm_parameter.private-subnet-ids.value)
  desiredCount                   = var.desired_count

  auto_scaler_config = {
    max_capacity               = var.auto_scaler_config.max_capacity
    min_capacity               = var.auto_scaler_config.min_capacity
    enable_cpu_scaling         = var.auto_scaler_config.enable_cpu_scaling
    cpu_scale_out_target_value = var.auto_scaler_config.cpu_scale_out_target_value
    cpu_scale_out_cooldown     = var.auto_scaler_config.cpu_scale_out_cooldown
    cpu_scale_in_target_value  = var.auto_scaler_config.cpu_scale_in_target_value
    cpu_scale_in_cooldown      = var.auto_scaler_config.cpu_scale_in_cooldown
  }
}