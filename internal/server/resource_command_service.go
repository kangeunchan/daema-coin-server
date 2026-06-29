package server

import (
	"context"
	"errors"
)

var errResourceNotFound = errors.New("resource not found")

type resourceCommandStore interface {
	put(ctx context.Context, resource, id string, data map[string]any) (map[string]any, error)
	patch(ctx context.Context, resource, id string, patch map[string]any) (map[string]any, bool, error)
}

type resourceCommandService struct {
	store resourceCommandStore
}

func (s *server) resourceCommands() resourceCommandService {
	return resourceCommandService{store: s.store}
}

type createResourceCommand struct {
	Resource     string
	Prefix       string
	Body         map[string]any
	Extras       map[string]any
	IDCandidates []string
}

func (cmd createResourceCommand) payload() (string, map[string]any) {
	body := cloneMap(cmd.Body)
	for key, value := range cmd.Extras {
		body[key] = value
	}
	return resourceID(body, cmd.Prefix, cmd.IDCandidates...), body
}

type patchResourceCommand struct {
	Resource string
	ID       string
	Body     map[string]any
	Extras   map[string]any
}

func (cmd patchResourceCommand) payload() map[string]any {
	body := cloneMap(cmd.Body)
	for key, value := range cmd.Extras {
		body[key] = value
	}
	return body
}

func (svc resourceCommandService) Create(ctx context.Context, cmd createResourceCommand) (map[string]any, error) {
	id, body := cmd.payload()
	return svc.store.put(ctx, cmd.Resource, id, body)
}

func (svc resourceCommandService) Patch(ctx context.Context, cmd patchResourceCommand) (map[string]any, error) {
	item, found, err := svc.store.patch(ctx, cmd.Resource, cmd.ID, cmd.payload())
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errResourceNotFound
	}
	return item, nil
}

func (svc resourceCommandService) Put(ctx context.Context, cmd patchResourceCommand) (map[string]any, error) {
	return svc.store.put(ctx, cmd.Resource, cmd.ID, cmd.payload())
}
