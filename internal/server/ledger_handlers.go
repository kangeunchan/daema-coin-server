package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *server) handleLedgerCalendar(w http.ResponseWriter, r *http.Request) {
	month := envDefault(r.URL.Query().Get("month"), time.Now().In(appLocation()).Format("2006-01"))
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), 1000)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 달력 데이터를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	byDate := map[string]map[string]int{}
	for _, item := range items {
		occurredAt := stringValue(item["occurredAt"])
		if len(occurredAt) < len("2006-01-02") || !strings.HasPrefix(occurredAt, month) {
			continue
		}
		date := occurredAt[:10]
		if byDate[date] == nil {
			byDate[date] = map[string]int{"income": 0, "expense": 0}
		}
		direction := stringValue(item["direction"])
		if direction != "income" {
			direction = "expense"
		}
		byDate[date][direction] += amountValue(item)
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	out := []map[string]any{}
	for _, date := range dates {
		day, _ := strconv.Atoi(date[len(date)-2:])
		row := map[string]any{"date": date, "day": day, "active": true}
		if byDate[date]["income"] > 0 {
			row["income"] = amount("DMC", byDate[date]["income"])
		}
		if byDate[date]["expense"] > 0 {
			row["expense"] = amount("DMC", byDate[date]["expense"])
		}
		out = append(out, row)
	}
	s.ok(w, r, out)
}

func (s *server) handleLedgerTransactions(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50)
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), limit)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 내역을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
}

func (s *server) handleLedgerAnalysis(w http.ResponseWriter, r *http.Request) {
	month := envDefault(r.URL.Query().Get("month"), time.Now().In(appLocation()).Format("2006-01"))
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), 1000)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 분석 데이터를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	incomeTotal := 0
	expenseTotal := 0
	incomeCategories := map[string]int{}
	expenseCategories := map[string]int{}
	labels := map[string]string{}
	for _, item := range items {
		occurredAt := stringValue(item["occurredAt"])
		if len(occurredAt) < len("2006-01") || !strings.HasPrefix(occurredAt, month) {
			continue
		}
		value := amountValue(item)
		category := envDefault(stringValue(item["categoryId"]), stringValue(item["type"]))
		if category == "" {
			category = "uncategorized"
		}
		labels[category] = envDefault(stringValue(item["categoryLabel"]), category)
		if stringValue(item["direction"]) == "income" {
			incomeTotal += value
			incomeCategories[category] += value
		} else {
			expenseTotal += value
			expenseCategories[category] += value
		}
	}
	categoryRows := func(values map[string]int) []map[string]any {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rows := []map[string]any{}
		for _, key := range keys {
			rows = append(rows, map[string]any{"id": key, "label": labels[key], "value": amount("DMC", values[key]), "color": ""})
		}
		return rows
	}
	s.ok(w, r, map[string]any{
		"month":             month,
		"incomeTotal":       amount("DMC", incomeTotal),
		"expenseTotal":      amount("DMC", expenseTotal),
		"incomeCategories":  categoryRows(incomeCategories),
		"expenseCategories": categoryRows(expenseCategories),
	})
}

func (s *server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceFeatures, 100)
}
