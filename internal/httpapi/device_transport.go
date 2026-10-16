package httpapi

import "errors"

func deviceTransport(value string) (string, error) {
	switch value {
	case "", "ws":
		return "ws", nil
	case "usb_reverse":
		return value, nil
	default:
		return "", errors.New("invalid_device_transport")
	}
}
