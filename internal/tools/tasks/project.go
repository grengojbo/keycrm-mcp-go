// SPDX-License-Identifier: AGPL-3.0-only

package tasks

// TaskSummary — компактна проєкція завдання для list_tasks: розбирається
// прямо з відповіді keyCRM, зайві поля (опис, чек-лист, налаштування) відкидаються.
type TaskSummary struct {
	ID           int    `json:"id"`
	ParentID     *int   `json:"parent_id,omitempty"`
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	Status       string `json:"status,omitempty"`
	Priority     int    `json:"priority,omitempty"`
	IsExpired    bool   `json:"is_expired"`
	ScheduledAt  string `json:"scheduled_at,omitempty"`
	ClosedAt     string `json:"closed_at,omitempty"`
	AssignedToID int    `json:"assigned_to_id,omitempty"`
	TaskedType   string `json:"tasked_type,omitempty"`
	TaskedID     *int   `json:"tasked_id,omitempty"`
}
