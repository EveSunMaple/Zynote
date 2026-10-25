package pptextract

type RequestError struct{ Message string }

func (value *RequestError) Error() string { return value.Message }

func requestError(message string) error { return &RequestError{Message: message} }
