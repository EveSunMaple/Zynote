package contract

// TranscriptionProvenance records observed request/adapter facts. AcquisitionSHA256
// binds the declaration file only; it does not attest loaded model weights.
type TranscriptionProvenance struct {
	Version             int    `json:"version"`
	RequestedLanguage   string `json:"requestedLanguage"`
	EngineAdapter       string `json:"engineAdapter"`
	AdapterSchema       int    `json:"adapterSchema"`
	RuntimeVersionState string `json:"runtimeVersionState"`
	DeclaredModelLabel  string `json:"declaredModelLabel"`
	ModelIdentityState  string `json:"modelIdentityState"`
	AcquisitionSHA256   string `json:"acquisitionSHA256"`
}
