package bilibili

import (
	"encoding/base64"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

func newQRCode(value string) (string, error) {
	code, err := qrcode.New(value, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("扫码地址无效")
	}
	data, err := code.PNG(-5)
	if err != nil {
		return "", fmt.Errorf("二维码生成失败")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}
