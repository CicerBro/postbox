package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"postbox/internal/awsbrand"
	"postbox/internal/events"
)

func runBrandPollers(ctx context.Context, provider *awsbrand.Provider, service *events.Service) error {
	type active struct {
		route  awsbrand.EventRoute
		cancel context.CancelFunc
	}
	pollers := map[int64]active{}
	var wg sync.WaitGroup
	defer func() {
		for _, poller := range pollers {
			poller.cancel()
		}
		wg.Wait()
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		routes, err := provider.EventRoutes(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("brand event routes could not be loaded", "error", err)
		}
		if err == nil {
			wanted := map[int64]awsbrand.EventRoute{}
			for _, route := range routes {
				if route.QueueURL != "" {
					wanted[route.BrandID] = route
				}
			}
			for id, poller := range pollers {
				if route, ok := wanted[id]; !ok || route != poller.route {
					poller.cancel()
					delete(pollers, id)
				}
			}
			for id, route := range wanted {
				if _, ok := pollers[id]; ok {
					continue
				}
				pollCtx, cancel := context.WithCancel(ctx)
				pollers[id] = active{route: route, cancel: cancel}
				wg.Add(1)
				go func() {
					defer wg.Done()
					for pollCtx.Err() == nil {
						client, err := provider.SQSClient(pollCtx, route.BrandID)
						if err == nil {
							err = service.PollBrand(pollCtx, client, route.BrandID, route.TopicARN, route.QueueURL)
						}
						if err != nil && pollCtx.Err() == nil {
							slog.Error("brand SQS polling failed", "brand_id", route.BrandID, "error", err)
						}
						select {
						case <-pollCtx.Done():
						case <-time.After(5 * time.Second):
						}
					}
				}()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
