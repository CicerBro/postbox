ALTER TABLE brand_aws_settings ADD COLUMN sns_topic_arn TEXT NOT NULL DEFAULT '';
ALTER TABLE brand_aws_settings ADD COLUMN sqs_queue_url TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX brand_aws_topic ON brand_aws_settings(sns_topic_arn) WHERE sns_topic_arn!='';
CREATE UNIQUE INDEX brand_aws_queue ON brand_aws_settings(sqs_queue_url) WHERE sqs_queue_url!='';
