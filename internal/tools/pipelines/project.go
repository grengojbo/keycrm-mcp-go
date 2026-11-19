// SPDX-License-Identifier: AGPL-3.0-only

package pipelines

import "strings"

// CardSummary — компактна проєкція картки воронки для list_pipeline_cards.
// Повні дані (товари, оплати, контакт, коментар менеджера) — через
// get_pipeline_card. Ім'я контакту тут не маскується: це вихід для моделі, а не лог.
type CardSummary struct {
	ID            int    `json:"id"`
	Title         string `json:"title,omitempty"`
	PipelineID    int    `json:"pipeline_id,omitempty"`
	StatusID      int    `json:"status_id,omitempty"`
	Status        string `json:"status,omitempty"`
	SourceID      int    `json:"source_id,omitempty"`
	ManagerID     int    `json:"manager_id,omitempty"`
	ContactName   string `json:"contact_name,omitempty"`
	ClientID      int    `json:"client_id,omitempty"`
	CommunicateAt string `json:"communicate_at,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// rawCard — поля картки keyCRM, з яких будується CardSummary.
type rawCard struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	StatusID      int    `json:"status_id"`
	SourceID      int    `json:"source_id"`
	ManagerID     int    `json:"manager_id"`
	CommunicateAt string `json:"communicate_at"`
	CreatedAt     string `json:"created_at"`
	Status        *struct {
		PipelineID int    `json:"pipeline_id"`
		Title      string `json:"title"`
	} `json:"status"`
	Contact *struct {
		FullName string `json:"full_name"`
		ClientID int    `json:"client_id"`
	} `json:"contact"`
}

func summarize(c rawCard) CardSummary {
	s := CardSummary{
		ID:            c.ID,
		Title:         c.Title,
		StatusID:      c.StatusID,
		SourceID:      c.SourceID,
		ManagerID:     c.ManagerID,
		CommunicateAt: c.CommunicateAt,
		CreatedAt:     c.CreatedAt,
	}
	if c.Status != nil {
		s.PipelineID = c.Status.PipelineID
		s.Status = c.Status.Title
	}
	if c.Contact != nil {
		s.ContactName = c.Contact.FullName
		s.ClientID = c.Contact.ClientID
	}
	return s
}

// Item — елемент списку воронок чи статусів: поля keyCRM без службових дат.
type Item = map[string]any

// compact прибирає службові дати (*_at) і порожні значення, щоб не витрачати
// контекст моделі. Ідентифікатори, назви, alias і прапорці лишаються.
func compact(it Item) Item {
	for k, v := range it {
		if v == nil || strings.HasSuffix(k, "_at") {
			delete(it, k)
		}
	}
	return it
}
