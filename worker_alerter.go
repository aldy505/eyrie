package main

import "time"

type AlertMessage struct {
	MonitorID  string    `json:"monitor_id"`
	Name       string    `json:"name"`
	Reason     string    `json:"reason"`
	OccurredAt time.Time `json:"occurred_at"`
}
