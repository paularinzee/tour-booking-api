package paystack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type PaymentService struct {
	apiKey     string
	httpClient *http.Client
}

type InitializePaymentRequest struct {
	Email       string                 `json:"email"`
	Amount      int64                  `json:"amount"`
	Reference   string                 `json:"reference"`
	CallbackURL string                 `json:"callback_url"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type InitializePaymentResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	Reference        string `json:"reference"`
	AccessCode       string `json:"access_code"`
}

type VerifyPaymentResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Status      string `json:"status"`
		Reference   string `json:"reference"`
		Amount      int64  `json:"amount"`
		Currency    string `json:"currency"`
		GatewayUsed string `json:"gateway_used"`
	} `json:"data"`
}

func NewPaymentService() (*PaymentService, error) {
	apiKey := os.Getenv("PAYSTACK_SECRET_KEY")
	fmt.Printf("DEBUG: Paystack API Key loaded, length: %d\n", len(apiKey))
	if apiKey == "" {
		return nil, fmt.Errorf("PAYSTACK_SECRET_KEY is not set in environment")
	}
	// Don't print the full key, just first few chars
	if len(apiKey) > 10 {
		fmt.Printf("DEBUG: Key starts with: %s...\n", apiKey[:10])
	}
	return &PaymentService{
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}, nil
}

// InitializePayment creates a Paystack transaction and returns checkout URL
func (s *PaymentService) InitializePayment(ctx context.Context, req *InitializePaymentRequest) (*InitializePaymentResponse, error) {
	url := "https://api.paystack.co/transaction/initialize"

	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Status  bool                      `json:"status"`
		Message string                    `json:"message"`
		Data    InitializePaymentResponse `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Status {
		return nil, fmt.Errorf("failed to initialize transaction: %s", result.Message)
	}

	return &result.Data, nil
}

// VerifyPayment verifies a Paystack transaction
func (s *PaymentService) VerifyPayment(ctx context.Context, reference string) (bool, error) {
	url := fmt.Sprintf("https://api.paystack.co/transaction/verify/%s", reference)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	var result VerifyPaymentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.Status == "success" && result.Data.Status == "success", nil
}
