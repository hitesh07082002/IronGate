package common

import (
	"log/slog"
	"os"
	"strconv"
)

func ServicePortFromEnv(defaultPort int) int {
	portValue := os.Getenv("PORT")
	if portValue == "" {
		return defaultPort
	}

	port, err := strconv.Atoi(portValue)
	if err != nil || port <= 0 || port > 65535 {
		slog.Warn("invalid PORT value, using default", "value", portValue, "default", defaultPort)
		return defaultPort
	}

	return port
}
