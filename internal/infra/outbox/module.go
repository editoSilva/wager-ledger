package outbox

import "go.uber.org/fx"

var Module = fx.Module("outbox", fx.Provide(NewPublisher), fx.Invoke(func(*Publisher) {}))
