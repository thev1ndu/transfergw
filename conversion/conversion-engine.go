package conversion

import (
	"context"
)

type Engine struct {
	translators map[string]Translator
}

type Translator interface {
	Translate(key, value string) (string, map[string]interface{}, error)
}

type ConversionResult struct {
	Success bool
	Issues  []Issue
}

type Issue struct {
	Severity string
	Message  string
}

func NewEngine() *Engine {
	return &Engine{
		translators: make(map[string]Translator),
	}
}

func (e *Engine) ConvertIngress(ctx context.Context, name string, spec interface{}) (*ConversionResult, error) {
	return &ConversionResult{
		Success: true,
		Issues:  []Issue{},
	}, nil
}

func (e *Engine) RegisterTranslator(annotation string, t Translator) {
	e.translators[annotation] = t
}

type RateLimitTranslator struct{}

func (t *RateLimitTranslator) Translate(key, value string) (string, map[string]interface{}, error) {
	return key, map[string]interface{}{}, nil
}

type RewriteTranslator struct{}

func (t *RewriteTranslator) Translate(key, value string) (string, map[string]interface{}, error) {
	return key, map[string]interface{}{}, nil
}

type AuthTranslator struct{}

func (t *AuthTranslator) Translate(key, value string) (string, map[string]interface{}, error) {
	return key, map[string]interface{}{}, nil
}

type CertManagerTranslator struct{}

func (t *CertManagerTranslator) Translate(key, value string) (string, map[string]interface{}, error) {
	return key, map[string]interface{}{}, nil
}
