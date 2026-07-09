package server

import (
	"testing"
	"time"
)

func TestPointConversionDueAtStartsAtNineOnJulyTenthKST(t *testing.T) {
	before := time.Date(2026, time.July, 10, 8, 59, 59, 0, pointConversionAtKST.Location())
	at := time.Date(2026, time.July, 10, 9, 0, 0, 0, pointConversionAtKST.Location())

	if pointConversionDueAt(before) {
		t.Fatal("pointConversionDueAt before cutoff = true, want false")
	}
	if !pointConversionDueAt(at) {
		t.Fatal("pointConversionDueAt at cutoff = false, want true")
	}
}

func TestWorldcupPredictionOpenAtClosesAtPointConversion(t *testing.T) {
	before := time.Date(2026, time.July, 10, 8, 59, 59, 0, pointConversionAtKST.Location())
	at := time.Date(2026, time.July, 10, 9, 0, 0, 0, pointConversionAtKST.Location())

	if !worldcupPredictionOpenAt(before) {
		t.Fatal("worldcupPredictionOpenAt before cutoff = false, want true")
	}
	if worldcupPredictionOpenAt(at) {
		t.Fatal("worldcupPredictionOpenAt at cutoff = true, want false")
	}
}

func TestWorldcupScheduleVisibleAtEndsAfterJulyTenthKST(t *testing.T) {
	onLastDay := time.Date(2026, time.July, 10, 23, 59, 59, 0, worldcupPredictionEndAtKST.Location())
	afterLastDay := time.Date(2026, time.July, 11, 0, 0, 0, 0, worldcupPredictionEndAtKST.Location())

	if !worldcupScheduleVisibleAt(onLastDay) {
		t.Fatal("worldcupScheduleVisibleAt on July 10 = false, want true")
	}
	if worldcupScheduleVisibleAt(afterLastDay) {
		t.Fatal("worldcupScheduleVisibleAt after July 10 = true, want false")
	}
}

func TestEffectiveWalletCurrencyAtConvertsPointAfterCutoff(t *testing.T) {
	before := time.Date(2026, time.July, 10, 8, 59, 59, 0, pointConversionAtKST.Location())
	after := time.Date(2026, time.July, 10, 9, 0, 0, 0, pointConversionAtKST.Location())

	if currency, original := effectiveWalletCurrencyAt("POINT", before); currency != "POINT" || original != "" {
		t.Fatalf("effectiveWalletCurrencyAt before cutoff = %s/%s, want POINT/empty", currency, original)
	}
	if currency, original := effectiveWalletCurrencyAt("POINT", after); currency != "DMC" || original != "POINT" {
		t.Fatalf("effectiveWalletCurrencyAt after cutoff = %s/%s, want DMC/POINT", currency, original)
	}
	if currency, original := effectiveWalletCurrencyAt("DMC", after); currency != "DMC" || original != "" {
		t.Fatalf("effectiveWalletCurrencyAt DMC after cutoff = %s/%s, want DMC/empty", currency, original)
	}
}
