package contract

// RequestedGenerationConfig describes effective outgoing request configuration,
// never credentials or a claim about the model actually serving the request.
type RequestedGenerationConfig struct {
	Version             int            `json:"version"`
	APIFormat           string         `json:"apiFormat"`
	EndpointSHA256      string         `json:"endpointSHA256"`
	RequestedModel      string         `json:"requestedModel"`
	EffectiveParameters map[string]any `json:"effectiveParameters"`
	ServerModelState    string         `json:"serverModelState"`
	AccountScopeState   string         `json:"accountScopeState"`
}

type JSONGenerationPolicy struct {
	Version              int    `json:"version"`
	Encoding             string `json:"encoding"`
	MaxAttempts          int    `json:"maxAttempts"`
	RepairFeedbackSHA256 string `json:"repairFeedbackSHA256"`
}
