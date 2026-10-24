package visual

type RequestError struct{ Message string }

func (value *RequestError) Error() string { return value.Message }

func badRequest(message string) error { return &RequestError{Message: message} }
