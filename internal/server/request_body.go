package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (s *server) decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes())
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, http.ErrBodyReadAfterClose) {
			return true
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "요청 본문이 너무 큽니다.", map[string]any{"limit": maxBytesErr.Limit})
			return false
		}
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", map[string]any{"cause": err.Error()})
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != nil && !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "요청 본문이 너무 큽니다.", map[string]any{"limit": maxBytesErr.Limit})
			return false
		}
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", map[string]any{"cause": err.Error()})
		return false
	} else if err == nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "JSON 본문에는 하나의 값만 포함할 수 있습니다.", nil)
		return false
	}
	return true
}
