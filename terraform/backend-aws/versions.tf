terraform {
  required_version = ">= 1.7.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      # 6.x: aws_lambda_permission.invoked_via_function_url, which a public
      # function URL needs since October 2025 (see main.tf).
      version = "~> 6.0"
    }
    # Zips backend_binary with the executable bit set.
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.4"
    }
  }
}

provider "aws" {
  region = var.region
}
