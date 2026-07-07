package webchat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const maxChatBodyBytes = 1 << 20 // 1 MiB

var errProjectNotAllowed = errors.New("project not allowed")

func (s *Server) decodeChatRequest(w http.ResponseWriter, r *http.Request) (chatRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return chatRequest{}, err
	}
	return req, nil
}

func (s *Server) resolveProject(reqProject string) (string, error) {
	if reqProject == "" {
		if s.project == "" {
			return "", fmt.Errorf("project is required")
		}
		return s.project, nil
	}
	if s.project != "" && reqProject != s.project {
		return "", errProjectNotAllowed
	}
	return reqProject, nil
}

func publicChatError(err error) string {
	if errors.Is(err, errProjectNotAllowed) {
		return "project not allowed"
	}
	return "chat request failed"
}
