// SPDX-License-Identifier: AGPL-3.0-only

package tasks

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
)

func connect(t *testing.T, h http.HandlerFunc, readOnly bool) *mcp.ClientSession {
	t.Helper()
	return server.Connect(t, []server.Toolset{Toolset}, server.Deps{KeyCRM: server.FakeKeyCRM(t, h)}, readOnly)
}

// noCalls — фейковий keyCRM, що рахує запити (їх не має бути).
func noCalls(calls *atomic.Int32) http.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) { calls.Add(1) }
}

func TestListTasksFiltersAndPaging(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/tasks" || q.Get("limit") != "50" || q.Get("page") != "2" || q.Get("sort") != "scheduled_at" {
			t.Errorf("неочікуваний запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if q.Get("filter[status]") != "open" || q.Get("filter[is_expired]") != "true" || q.Get("filter[type]") != "call,meeting" ||
			q.Get("filter[priority]") != "2,3" || q.Get("filter[tasked_type]") != "order" || q.Get("filter[tasked_id]") != "1024" {
			t.Errorf("фільтри: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"total":51,"current_page":2,"data":[{"id":4090,"parent_id":null,"name":"Передзвонити",
			"description":"Довгий опис","type":"call","status":"open","priority":2,"is_expired":true,"scheduled_at":"2026-11-18T09:00:00.000000Z",
			"closed_at":null,"assigned_to_id":12,"assigned_by_id":3,"tasked_type":"order","tasked_id":1024,
			"checklist":[{"value":"x","checked":false}]}],"next_page_url":"x?page=3"}`)
	}, false)
	res := server.CallTool(t, cs, "list_tasks", map[string]any{
		"status": "open", "is_expired": true, "type": []string{"call", "meeting"}, "priority": []int{2, 3},
		"tasked_type": "order", "tasked_id": []int{1024}, "sort": "scheduled_at", "limit": 70, "page": 2,
	})
	out := server.Decode[listing.Result[TaskSummary]](t, res)
	if out.Note != "limit capped at 50" || !out.HasNext || out.Total != 51 || len(out.Items) != 1 {
		t.Fatalf("сторінка: %+v", out)
	}
	it := out.Items[0]
	if it.ID != 4090 || it.Name != "Передзвонити" || it.Priority != 2 || !it.IsExpired || it.TaskedID == nil || *it.TaskedID != 1024 || it.ParentID != nil {
		t.Errorf("проєкція: %+v", it)
	}
	if strings.Contains(server.ResultText(res), "Довгий опис") {
		t.Error("проєкція не має містити опис")
	}
}

func TestListTasksValidation(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, noCalls(&calls), false)
	for name, args := range map[string]map[string]any{
		"tasked_id без tasked_type": {"tasked_id": []int{1}},
		"пріоритет поза 1–3":        {"priority": []int{4}},
		"невідомий статус":          {"status": "closed"},
		"невідомий тип":             {"type": []string{"todo"}},
	} {
		t.Run(name, func(t *testing.T) {
			if res := server.CallTool(t, cs, "list_tasks", args); !res.IsError {
				t.Errorf("очікувалась помилка: %s", server.ResultText(res))
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("запитів до keyCRM: %d", calls.Load())
	}
}

func TestGetTaskDefaultInclude(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/4090" || r.URL.Query().Get("include") != "sub_tasks,attachments" {
			t.Errorf("запит %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"id":4090,"sub_tasks":[]}`)
	}, false)
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "get_task", map[string]any{"task_id": 4090}))
	if out["id"] != float64(4090) {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateTaskSendsBody(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks" {
			t.Errorf("запит %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["assigned_to_id"] != float64(12) || body["assigned_by_id"] != float64(3) || body["tasked_type"] != "order" ||
			body["priority"] != float64(2) || body["settings"].(map[string]any)["allow_editing"] != true {
			t.Errorf("тіло: %v", body)
		}
		if cl, _ := body["checklist"].([]any); len(cl) != 1 || cl[0].(map[string]any)["value"] != "Перевірити виписку" {
			t.Errorf("чек-лист: %v", body["checklist"])
		}
		if _, ok := body["parent_id"]; ok {
			t.Error("parent_id не задано — не має бути в тілі")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":4091}`)
	}, false)
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "create_task", map[string]any{
		"name": "Передзвонити", "priority": 2, "assigned_to_id": 12, "assigned_by_id": 3,
		"tasked_type": "order", "tasked_id": 1024, "settings": map[string]any{"allow_editing": true},
		"checklist": []map[string]any{{"value": "Перевірити виписку"}},
	}))
	if out["id"] != float64(4091) {
		t.Errorf("результат: %v", out)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, noCalls(&calls), false)
	for name, args := range map[string]map[string]any{
		"без постановника":   {"assigned_to_id": 12},
		"tasked_type без id": {"assigned_to_id": 12, "assigned_by_id": 3, "tasked_type": "order"},
		"пріоритет поза 1–3": {"assigned_to_id": 12, "assigned_by_id": 3, "priority": 5},
		"невідома прив'язка": {"assigned_to_id": 12, "assigned_by_id": 3, "tasked_type": "product", "tasked_id": 1},
	} {
		t.Run(name, func(t *testing.T) {
			if res := server.CallTool(t, cs, "create_task", args); !res.IsError {
				t.Errorf("очікувалась помилка: %s", server.ResultText(res))
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("запитів до keyCRM: %d", calls.Load())
	}
}

func TestCreateTask422IsError(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"The given data was invalid.","errors":{"assigned_to_id":["Користувач неактивний."]}}`)
	}, false)
	res := server.CallTool(t, cs, "create_task", map[string]any{"assigned_to_id": 99, "assigned_by_id": 3})
	if !res.IsError || !strings.Contains(server.ResultText(res), "assigned_to_id: Користувач неактивний.") {
		t.Errorf("помилка: %s", server.ResultText(res))
	}
}

func TestUpdateTaskBody(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"закриття", map[string]any{"status": "done", "closed_by_id": 15}, `{"closed_by_id":15,"status":"done"}`},
		{"відв'язування", map[string]any{"unbind": true}, `{"tasked_id":null,"tasked_type":null}`},
		{"очищення чек-листа", map[string]any{"clear_checklist": true}, `{"checklist":[]}`},
		{"порожня назва", map[string]any{"name": ""}, `{"name":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/tasks/4090" {
					t.Errorf("запит %s %s", r.Method, r.URL.Path)
				}
				b, _ := io.ReadAll(r.Body)
				if string(b) != tt.want {
					t.Errorf("тіло %s, очікувалось %s", b, tt.want)
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"id":4090}`)
			}, false)
			args := map[string]any{"task_id": 4090}
			for k, v := range tt.args {
				args[k] = v
			}
			server.Decode[map[string]any](t, server.CallTool(t, cs, "update_task", args))
		})
	}
}

func TestUpdateTaskValidation(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, noCalls(&calls), false)
	for name, args := range map[string]map[string]any{
		"нічого не змінюється":        {},
		"closed_by_id без done":       {"closed_by_id": 15},
		"unbind разом з прив'язкою":   {"unbind": true, "tasked_type": "order", "tasked_id": 1},
		"clear_checklist з checklist": {"clear_checklist": true, "checklist": []map[string]any{{"value": "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			args["task_id"] = 4090
			if res := server.CallTool(t, cs, "update_task", args); !res.IsError {
				t.Errorf("очікувалась помилка: %s", server.ResultText(res))
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("запитів до keyCRM: %d", calls.Load())
	}
}

func TestImportTasksNeedsConfirm(t *testing.T) {
	var calls atomic.Int32
	cs := connect(t, noCalls(&calls), false)
	task := map[string]any{"name": "Передзвонити", "assigned_to_id": 12, "assigned_by_id": 3}
	res := server.CallTool(t, cs, "import_tasks", map[string]any{"tasks": []any{task}})
	if !res.IsError || !strings.Contains(server.ResultText(res), "confirm: true") {
		t.Errorf("без confirm: %s", server.ResultText(res))
	}
	many := make([]any, maxImport+1)
	for i := range many {
		many[i] = task
	}
	if res := server.CallTool(t, cs, "import_tasks", map[string]any{"tasks": many, "confirm": true}); !res.IsError {
		t.Error("понад 50 завдань має бути помилкою")
	}
	bad := []any{task, map[string]any{"assigned_to_id": 12, "assigned_by_id": 0}}
	if res := server.CallTool(t, cs, "import_tasks", map[string]any{"tasks": bad, "confirm": true}); !res.IsError ||
		!strings.Contains(server.ResultText(res), "tasks[1].") {
		t.Errorf("невалідне завдання: %s", server.ResultText(res))
	}
	if calls.Load() != 0 {
		t.Errorf("запитів до keyCRM: %d", calls.Load())
	}
}

func TestImportTasksSendsBody(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks/import" {
			t.Errorf("запит %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Tasks []map[string]any `json:"tasks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Tasks) != 2 {
			t.Errorf("тіло: %+v (%v)", body, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"status":true,"data":[{"id":1},{"id":2}]}`)
	}, false)
	task := map[string]any{"assigned_to_id": 12, "assigned_by_id": 3}
	out := server.Decode[map[string]any](t, server.CallTool(t, cs, "import_tasks", map[string]any{"tasks": []any{task, task}, "confirm": true}))
	if out["status"] != true {
		t.Errorf("результат: %v", out)
	}
}

func TestTaskFilePaths(t *testing.T) {
	tests := []struct{ tool, method string }{
		{"attach_task_file", http.MethodPost},
		{"detach_task_file", http.MethodDelete},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != "/tasks/4090/attachment/73609" {
					t.Errorf("запит %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}, false)
			out := server.Decode[map[string]any](t, server.CallTool(t, cs, tt.tool, map[string]any{"task_id": 4090, "file_id": 73609}))
			if out["ok"] != true {
				t.Errorf("результат: %v", out)
			}
		})
	}
}

func TestReadOnlyTasks(t *testing.T) {
	cs := connect(t, func(http.ResponseWriter, *http.Request) {}, true)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "get_task,list_tasks" && strings.Join(names, ",") != "list_tasks,get_task" {
		t.Errorf("read-only інструменти: %v", names)
	}
}
