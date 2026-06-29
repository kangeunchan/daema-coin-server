package server

import (
	"context"
	"time"
)

type adminResourceService struct {
	resources resourceCommandService
}

func (s *server) adminResources() adminResourceService {
	return adminResourceService{resources: s.resourceCommands()}
}

func (svc adminResourceService) CreateFestival(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceFestivals, "festival", body, nil, "festivalId")
}

func (svc adminResourceService) UpdateFestival(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceFestivals, id, body, nil)
}

func (svc adminResourceService) CreateBooth(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceBooths, "booth", body, nil, "boothId")
}

func (svc adminResourceService) UpdateBooth(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceBooths, id, body, nil)
}

func (svc adminResourceService) CreateBoothCategory(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceBoothCategories, "category", body, nil, "categoryId")
}

func (svc adminResourceService) UpdateBoothCategory(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceBoothCategories, id, body, nil)
}

func (svc adminResourceService) CreateMap(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceMaps, "map", body, nil, "mapId")
}

func (svc adminResourceService) UpdateUser(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceUsers, id, body, nil)
}

func (svc adminResourceService) CreateRoleAssignment(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceRoleAssignments, "role-assignment", body, nil, "assignmentId")
}

func (svc adminResourceService) CreateRewardRule(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceRewardRules, "reward-rule", body, nil, "ruleId")
}

func (svc adminResourceService) UpdateRewardRule(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceRewardRules, id, body, nil)
}

func (svc adminResourceService) CreateNotice(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceNotices, "notice", body, nil, "noticeId")
}

func (svc adminResourceService) UpdateNotice(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceNotices, id, body, nil)
}

func (svc adminResourceService) CreatePromotion(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourcePromotions, "promotion", body, nil, "promotionId")
}

func (svc adminResourceService) UpdatePromotion(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourcePromotions, id, body, nil)
}

func (svc adminResourceService) CreateNotification(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceNotifications, "notification", body, map[string]any{"sentAt": time.Now().UTC().Format(time.RFC3339)}, "notificationId")
}

func (svc adminResourceService) CreateUpload(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceUploads, "upload", body, nil, "uploadId", "fileId")
}

func (svc adminResourceService) CreateWorldcupTeam(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceWorldcupTeams, "worldcup-team", body, nil, "teamId")
}

func (svc adminResourceService) CreateWorldcupMatch(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceWorldcupMatches, "worldcup-match", body, nil, "matchId")
}

func (svc adminResourceService) UpdateWorldcupMatch(ctx context.Context, id string, body map[string]any) (map[string]any, error) {
	return svc.patch(ctx, resourceWorldcupMatches, id, body, nil)
}

func (svc adminResourceService) PutWorldcupLineup(ctx context.Context, matchID string, body map[string]any) (map[string]any, error) {
	return svc.put(ctx, resourceWorldcupLineups, matchID, body, map[string]any{"matchId": matchID})
}

func (svc adminResourceService) PutWorldcupStats(ctx context.Context, matchID string, body map[string]any) (map[string]any, error) {
	return svc.put(ctx, resourceWorldcupStats, matchID, body, map[string]any{"matchId": matchID})
}

func (svc adminResourceService) CreateIncident(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceIncidents, "incident", body, nil, "incidentId")
}

func (svc adminResourceService) CreateRankingRule(ctx context.Context, body map[string]any) (map[string]any, error) {
	return svc.create(ctx, resourceRankingRules, "ranking-rule", body, nil, "ruleId")
}

func (svc adminResourceService) create(ctx context.Context, resource, prefix string, body, extras map[string]any, candidates ...string) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resource, Prefix: prefix, Body: body, Extras: extras, IDCandidates: candidates})
}

func (svc adminResourceService) patch(ctx context.Context, resource, id string, body, extras map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resource, ID: id, Body: body, Extras: extras})
}

func (svc adminResourceService) put(ctx context.Context, resource, id string, body, extras map[string]any) (map[string]any, error) {
	return svc.resources.Put(ctx, patchResourceCommand{Resource: resource, ID: id, Body: body, Extras: extras})
}
