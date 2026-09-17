package sqsinfra

import (
	"context"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func (c *Consumer) Name() string {
	return "sqs"
}

func (c *Consumer) Ready(ctx context.Context) error {
	_, err := c.client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: &c.queueURL})
	return err
}
