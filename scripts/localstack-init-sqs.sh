#!/bin/sh
set -eu

create_fifo_with_dlq() {
  queue_name="$1" dlq_name="$2"

  awslocal sqs create-queue --queue-name "$dlq_name" \
    --attributes '{"FifoQueue":"true","ContentBasedDeduplication":"false"}' >/dev/null

  dlq_arn=$(awslocal sqs get-queue-attributes \
    --queue-url "http://localhost:4566/000000000000/${dlq_name}" \
    --attribute-names QueueArn --query 'Attributes.QueueArn' --output text)

  redrive_policy=$(printf '{"deadLetterTargetArn":"%s","maxReceiveCount":"5"}' "$dlq_arn")
  attributes=$(printf '{"FifoQueue":"true","ContentBasedDeduplication":"false","VisibilityTimeout":"30","RedrivePolicy":"%s"}' \
    "$(printf '%s' "$redrive_policy" | sed 's/"/\\"/g')")

  awslocal sqs create-queue --queue-name "$queue_name" --attributes "$attributes" >/dev/null
}

create_fifo_with_dlq wager-transactions.fifo wager-transactions-dlq.fifo
create_fifo_with_dlq wager-events.fifo wager-events-dlq.fifo
