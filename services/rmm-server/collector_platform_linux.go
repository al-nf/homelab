package main

import "context"

func (s *sampler) sample(ctx context.Context) telemetry { return s.sampleLinux(ctx) }
