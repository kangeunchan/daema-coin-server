package server

import "testing"

func TestValidStudentNo(t *testing.T) {
	tests := map[string]bool{
		"1234":          true,
		"001234567890":  true,
		"123":           false,
		"1234567890123": false,
		"12 34":         false,
		"１２３４":          false,
	}
	for studentNo, want := range tests {
		if got := validStudentNo(studentNo); got != want {
			t.Errorf("validStudentNo(%q) = %v, want %v", studentNo, got, want)
		}
	}
}
