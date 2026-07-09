package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleCustomerPushTargetRegister(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	userID := s.currentUserID(r)
	target, targetType := pushTargetFromBody(body)
	if target == "" {
		s.fail(w, r, http.StatusBadRequest, "PUSH_TARGET_REQUIRED", "푸시 알림 대상 값이 필요합니다.", nil)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := pushTargetID(userID, targetType, target)
	item := cloneMap(body)
	item["target"] = target
	item["targetType"] = targetType
	item["userId"] = userID
	item["platform"] = firstNonEmpty(stringValue(body["platform"]), "web")
	item["status"] = "active"
	item["lastSeenAt"] = now

	created, err := s.resourceCommands().Put(r.Context(), patchResourceCommand{
		Resource: resourcePushTargets,
		ID:       id,
		Body:     item,
	})
	if err != nil {
		s.failResourceCommand(w, r, resourcePushTargets, id, "update", err)
		return
	}
	s.ok(w, r, created)
}

func (s *server) handleCustomerPushTargetDelete(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	userID := s.currentUserID(r)
	targetID := strings.TrimSpace(r.PathValue("targetId"))
	if targetID == "" {
		target, targetType := pushTargetFromBody(body)
		if target != "" {
			targetID = pushTargetID(userID, targetType, target)
		}
	}
	if targetID == "" {
		s.fail(w, r, http.StatusBadRequest, "PUSH_TARGET_REQUIRED", "삭제할 푸시 알림 대상 값이 필요합니다.", nil)
		return
	}

	existing, found, err := s.store.get(r.Context(), resourcePushTargets, targetID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "푸시 알림 대상을 읽지 못했습니다.", map[string]any{"targetId": targetID, "cause": err.Error()})
		return
	}
	if found && stringValue(existing["userId"]) != userID {
		s.fail(w, r, http.StatusNotFound, "PUSH_TARGET_NOT_FOUND", "푸시 알림 대상을 찾을 수 없습니다.", map[string]any{"targetId": targetID})
		return
	}
	if err := s.store.delete(r.Context(), resourcePushTargets, targetID); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "푸시 알림 대상을 삭제하지 못했습니다.", map[string]any{"targetId": targetID, "cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{"deleted": true, "targetId": targetID})
}

func pushTargetFromBody(body map[string]any) (string, string) {
	fid := strings.TrimSpace(stringValue(body["fid"]))
	token := strings.TrimSpace(firstNonEmpty(stringValue(body["target"]), stringValue(body["token"])))
	targetType := strings.ToLower(strings.TrimSpace(stringValue(body["targetType"])))
	if targetType == "" && fid != "" {
		targetType = "fid"
	}
	if targetType == "" {
		targetType = "token"
	}
	if targetType != "fid" && targetType != "token" {
		targetType = "token"
	}
	target := token
	if target == "" {
		target = fid
	}
	return target, targetType
}

func pushTargetID(userID, targetType, target string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{userID, targetType, target}, "\x00")))
	return "push-" + base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *server) activeCustomerPushTargets(ctx context.Context, userID string) ([]map[string]any, error) {
	targets, err := s.store.listFiltered(ctx, resourcePushTargets, []resourceFilter{{Field: "userId", Value: userID}}, 100)
	if err != nil {
		return nil, err
	}
	active := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		status := strings.ToLower(firstNonEmpty(stringValue(target["status"]), "active"))
		if status != "active" {
			continue
		}
		if value, targetType := pushTargetFromBody(target); value == "" || targetType != "token" {
			continue
		}
		active = append(active, target)
	}
	return active, nil
}

func orderCompletionStatus(body map[string]any) bool {
	status := strings.ToLower(strings.TrimSpace(stringValue(body["status"])))
	switch status {
	case "completed", "done", "fulfilled", "picked_up", "pickup_completed":
		return true
	default:
		return false
	}
}

func (s *server) enqueueOrderCompletionNotification(order, request map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), envDuration("FCM_NOTIFICATION_STORE_TIMEOUT", 3*time.Second))
	defer cancel()

	notification, err := s.createOrderCompletionNotification(ctx, order, request)
	if err != nil {
		slog.Warn("create order completion notification failed",
			"order_id", stringValue(order["id"]),
			"error", err,
		)
		return
	}

	go s.dispatchNotificationPush(notification)
}

func (s *server) createOrderCompletionNotification(ctx context.Context, order, request map[string]any) (map[string]any, error) {
	userID := firstNonEmpty(stringValue(order["userId"]), stringValue(order["customerId"]))
	orderID := stringValue(order["id"])
	if userID == "" || orderID == "" {
		return nil, fmt.Errorf("order completion notification requires user and order id")
	}

	title := notificationText(firstNonEmpty(stringValue(request["notificationTitle"]), "주문 픽업 완료"), 64)
	body := notificationText(firstNonEmpty(
		stringValue(request["notificationBody"]),
		stringValue(request["notificationMessage"]),
		stringValue(request["pickupMessage"]),
		defaultOrderCompletionMessage(order),
	), 180)
	now := time.Now().UTC().Format(time.RFC3339)
	notification := map[string]any{
		"type":       "order_completed",
		"userId":     userID,
		"customerId": userID,
		"orderId":    orderID,
		"boothId":    stringValue(order["boothId"]),
		"title":      title,
		"body":       body,
		"message":    body,
		"read":       false,
		"sentAt":     now,
		"pushStatus": "queued",
	}
	if link := customerWebLink("/booth/orders"); link != "" {
		notification["link"] = link
	}
	return s.resourceCommands().Put(ctx, patchResourceCommand{
		Resource: resourceNotifications,
		ID:       ledgerID("order-completed", orderID),
		Body:     notification,
	})
}

func defaultOrderCompletionMessage(order map[string]any) string {
	productName := firstNonEmpty(stringValue(order["productName"]), stringValue(order["item"]), "주문")
	return productName + " 픽업이 완료되었습니다."
}

func notificationText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func customerWebLink(path string) string {
	baseURL := strings.TrimRight(firstNonEmpty(
		env("FCM_WEB_PUSH_LINK_BASE_URL", ""),
		env("CUSTOMER_WEB_BASE_URL", ""),
		env("PUBLIC_BASE_URL", ""),
	), "/")
	if baseURL == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return baseURL + path
}

func (s *server) dispatchNotificationPush(notification map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), envDuration("FCM_SEND_TIMEOUT", 8*time.Second))
	defer cancel()

	notificationID := stringValue(notification["id"])
	userID := firstNonEmpty(stringValue(notification["userId"]), stringValue(notification["customerId"]))
	if notificationID == "" || userID == "" {
		return
	}

	targets, err := s.activeCustomerPushTargets(ctx, userID)
	if err != nil {
		s.patchNotificationPushStatus(ctx, notificationID, "failed", nil, err)
		return
	}
	if len(targets) == 0 {
		s.patchNotificationPushStatus(ctx, notificationID, "no_targets", []map[string]any{}, nil)
		return
	}
	if s.fcm == nil {
		s.patchNotificationPushStatus(ctx, notificationID, "not_configured", nil, errors.New("fcm is not configured"))
		return
	}

	results := make([]map[string]any, 0, len(targets))
	successes := 0
	for _, target := range targets {
		targetValue, targetType := pushTargetFromBody(target)
		targetID := stringValue(target["id"])
		result := map[string]any{"targetId": targetID, "targetType": targetType}
		name, err := s.fcm.Send(ctx, targetValue, notificationFCMMessage(notification))
		if err != nil {
			result["status"] = "failed"
			result["error"] = err.Error()
			var sendErr fcmSendError
			if errors.As(err, &sendErr) && (sendErr.StatusCode == http.StatusBadRequest || sendErr.StatusCode == http.StatusNotFound) {
				s.markPushTargetInvalid(ctx, targetID, err)
			}
		} else {
			result["status"] = "sent"
			result["messageName"] = name
			successes++
		}
		results = append(results, result)
	}

	status := "failed"
	if successes == len(targets) {
		status = "sent"
	} else if successes > 0 {
		status = "partial"
	}
	s.patchNotificationPushStatus(ctx, notificationID, status, results, nil)
}

func notificationFCMMessage(notification map[string]any) fcmMessage {
	data := map[string]string{
		"type":    stringValue(notification["type"]),
		"orderId": stringValue(notification["orderId"]),
		"boothId": stringValue(notification["boothId"]),
		"link":    stringValue(notification["link"]),
	}
	for key, value := range data {
		if value == "" {
			delete(data, key)
		}
	}
	message := fcmMessage{
		Data: data,
		Notification: fcmNotification{
			Title: stringValue(notification["title"]),
			Body:  stringValue(notification["body"]),
		},
	}
	if link := stringValue(notification["link"]); link != "" {
		message.Webpush = &fcmWebpush{FCMOptions: fcmWebpushOptions{Link: link}}
	}
	return message
}

func (s *server) patchNotificationPushStatus(ctx context.Context, notificationID, status string, results []map[string]any, err error) {
	patch := map[string]any{
		"pushStatus":     status,
		"pushFinishedAt": time.Now().UTC().Format(time.RFC3339),
	}
	if results != nil {
		patch["pushResults"] = results
	}
	if err != nil {
		patch["pushError"] = err.Error()
	}
	if _, _, patchErr := s.store.patch(ctx, resourceNotifications, notificationID, patch); patchErr != nil {
		slog.Warn("patch notification push status failed", "notification_id", notificationID, "error", patchErr)
	}
}

func (s *server) markPushTargetInvalid(ctx context.Context, targetID string, cause error) {
	if targetID == "" {
		return
	}
	patch := map[string]any{
		"status":     "invalid",
		"disabledAt": time.Now().UTC().Format(time.RFC3339),
		"error":      cause.Error(),
	}
	if _, _, err := s.store.patch(ctx, resourcePushTargets, targetID, patch); err != nil {
		slog.Warn("mark push target invalid failed", "target_id", targetID, "error", err)
	}
}
