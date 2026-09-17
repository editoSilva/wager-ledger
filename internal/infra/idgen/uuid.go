package idgen

import "github.com/google/uuid"

type UUIDGenerator struct{}

func NewUUIDGenerator() UUIDGenerator { return UUIDGenerator{} }

func (UUIDGenerator) NewID() string { return uuid.NewString() }
