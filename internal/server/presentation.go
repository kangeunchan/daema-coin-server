package server

import (
	"fmt"
	"strconv"
	"time"
)

func amount(currency string, value int) map[string]any {
	suffix := currency
	if currency == "POINT" {
		suffix = "P"
	}
	return map[string]any{"currency": currency, "value": value, "formatted": fmt.Sprintf("%s %s", number(value), suffix)}
}

func appLocation() *time.Location {
	location, err := time.LoadLocation(env("APP_TIMEZONE", "Asia/Seoul"))
	if err != nil {
		return time.FixedZone("Asia/Seoul", 9*60*60)
	}
	return location
}

func number(v int) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	s := strconv.Itoa(v)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}

func media(id, path, alt string) map[string]any {
	return map[string]any{"id": id, "url": path, "alt": alt}
}
