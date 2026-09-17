package sqsinfra

import "go.uber.org/fx"

var Module = fx.Module("sqs", fx.Provide(NewConsumer), fx.Invoke(func(*Consumer) {}))
