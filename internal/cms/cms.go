package cms

import (
	"fmt"

	"pcsvoip-cms/internal/ai"
	"pcsvoip-cms/internal/storage"
)

type Service struct {
	Storage    *storage.Service
	AIProvider ai.AIProvider
}

func NewService(store *storage.Service, provider ai.AIProvider) *Service {
	return &Service{
		Storage:    store,
		AIProvider: provider,
	}
}

func (s *Service) ListFiles(path string) ([]storage.FileInfo, error) {
	return s.Storage.ListFiles(path)
}

func (s *Service) LoadContent(path string) (string, error) {
	return s.Storage.ReadFile(path)
}

func (s *Service) SaveContent(path, content string) error {
	return s.Storage.WriteFile(path, content)
}

func (s *Service) AIEdit(path, instruction string) (string, error) {
	if s.AIProvider == nil {
		return "", fmt.Errorf("AI provider not configured")
	}

	content, err := s.Storage.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to load content: %w", err)
	}

	// AI returns JSON find/replace edits, not the full file
	response, err := s.AIProvider.GenerateContent(instruction, content)
	if err != nil {
		return "", fmt.Errorf("AI generation failed: %w", err)
	}

	// Parse the JSON edit operations
	edits, err := ai.ParseEditsResponse(response)
	if err != nil {
		return "", fmt.Errorf("AI response error: %w", err)
	}

	// Apply edits to original content
	result, err := ai.ApplyEdits(content, edits)
	if err != nil {
		return "", fmt.Errorf("failed to apply AI edits: %w", err)
	}

	// Return preview only — admin must approve before saving
	return result, nil
}
