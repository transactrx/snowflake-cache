variable "project_name" {
  type = string
}

terraform {
  backend "s3" {
    encrypt        = true
    region         = "us-east-1"
  }
}

resource "aws_ecr_repository" "repository" {
  name = "transactrx/${lower(var.project_name)}"
  image_scanning_configuration {
    scan_on_push = true
  }
}

