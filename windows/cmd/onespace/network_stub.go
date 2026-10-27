//go:build !windows

package main

import "context"

func watchNetworkChanges(ctx context.Context, changed func()) { <-ctx.Done() }
