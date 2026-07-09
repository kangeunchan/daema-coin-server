package server

import "time"

var (
	pointConversionAtKST       = time.Date(2026, time.July, 10, 9, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	worldcupPredictionEndAtKST = time.Date(2026, time.July, 11, 0, 0, 0, 0, time.FixedZone("KST", 9*60*60))
)

func pointConversionDueAt(now time.Time) bool {
	return !now.In(pointConversionAtKST.Location()).Before(pointConversionAtKST)
}

func worldcupPredictionOpenAt(now time.Time) bool {
	return now.In(pointConversionAtKST.Location()).Before(pointConversionAtKST)
}

func worldcupScheduleVisibleAt(now time.Time) bool {
	return now.In(worldcupPredictionEndAtKST.Location()).Before(worldcupPredictionEndAtKST)
}

func effectiveWalletCurrencyAt(currency string, now time.Time) (string, string) {
	if currency == "POINT" && pointConversionDueAt(now) {
		return "DMC", "POINT"
	}
	return currency, ""
}
