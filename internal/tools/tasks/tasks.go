// SPDX-License-Identifier: AGPL-3.0-only

// Package tasks — завдання keyCRM: список, перегляд, створення, оновлення
// (зокрема закриття), імпорт і файли завдання.
package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група tasks.
var Toolset = server.Toolset{Name: "tasks", Register: register}

// maxImport — найбільше завдань в одному import_tasks (ліміт keyCRM).
const maxImport = 50

// defaultInclude — асоціації get_task за замовчуванням.
var defaultInclude = []string{"sub_tasks", "attachments"}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// ListTasksInput — вхід list_tasks.
type ListTasksInput struct {
	listing.Page
	ID               []int    `json:"id,omitempty" jsonschema:"ідентифікатори завдань"`
	Status           string   `json:"status,omitempty" enum:"open,done" jsonschema:"open — відкриті, done — закриті; без фільтра — усі"`
	IsExpired        *bool    `json:"is_expired,omitempty" jsonschema:"true — лише протерміновані, false — лише непротерміновані"`
	Type             []string `json:"type,omitempty" enum:"task,remind,call,chat,meeting,event" jsonschema:"типи завдань"`
	Priority         []int    `json:"priority,omitempty" jsonschema:"пріоритети: 1 — звичайний, 2 — важливо, 3 — дуже важливо"`
	AssignedToID     []int    `json:"assigned_to_id,omitempty" jsonschema:"відповідальні (list_users)"`
	AssignedByID     []int    `json:"assigned_by_id,omitempty" jsonschema:"постановники (list_users)"`
	ClosedByID       []int    `json:"closed_by_id,omitempty" jsonschema:"хто закрив (list_users)"`
	TaskedType       string   `json:"tasked_type,omitempty" enum:"order,lead,client" jsonschema:"до чого прив'язане: order — замовлення, lead — картка воронки, client — покупець"`
	TaskedID         []int    `json:"tasked_id,omitempty" jsonschema:"ідентифікатори сутностей; лише разом з tasked_type"`
	ParentID         int      `json:"parent_id,omitempty" jsonschema:"батьківське завдання — повертає його підзадачі"`
	IsSubtask        *bool    `json:"is_subtask,omitempty" jsonschema:"true — лише підзадачі, false — лише завдання верхнього рівня"`
	ScheduledBetween string   `json:"scheduled_between,omitempty" jsonschema:"термін виконання (UTC) через кому: 2026-11-01 00:00:00,2026-11-07 23:59:59"`
	CreatedBetween   string   `json:"created_between,omitempty" jsonschema:"період створення (UTC) через кому, як scheduled_between"`
	UpdatedBetween   string   `json:"updated_between,omitempty" jsonschema:"період оновлення (UTC) через кому, як scheduled_between"`
	ClosedBetween    string   `json:"closed_between,omitempty" jsonschema:"період закриття (UTC) через кому, як scheduled_between"`
	Sort             string   `json:"sort,omitempty" enum:"id,-id,scheduled_at,-scheduled_at,created_at,-created_at,updated_at,-updated_at,closed_at,-closed_at,priority,-priority" jsonschema:"сортування; мінус — за спаданням"`
}

// GetTaskInput — вхід get_task.
type GetTaskInput struct {
	TaskID  int      `json:"task_id" jsonschema:"ідентифікатор завдання"`
	Include []string `json:"include,omitempty" enum:"assigned_to,assigned_by,closed_by,sub_tasks,parent,attachments" jsonschema:"асоціації; за замовчуванням sub_tasks і attachments"`
}

// ChecklistItem — пункт чек-листа.
type ChecklistItem struct {
	Value   string `json:"value" jsonschema:"текст пункту"`
	Checked bool   `json:"checked,omitempty" jsonschema:"true — виконано"`
}

// Settings — налаштування завдання.
type Settings struct {
	AllowEditing *bool `json:"allow_editing,omitempty" jsonschema:"дозволити відповідальному редагувати завдання в keyCRM"`
}

// TaskFields — тіло нового завдання (createTask, елемент importTasks).
type TaskFields struct {
	Name         string          `json:"name,omitempty" jsonschema:"назва, до 255 символів (за замовчуванням «Задача»)"`
	Description  string          `json:"description,omitempty" jsonschema:"опис"`
	Type         string          `json:"type,omitempty" enum:"task,remind,call,chat,meeting,event" jsonschema:"тип: task — завдання, remind — нагадування, call — дзвінок, chat — повідомлення, meeting — зустріч, event — подія (за замовчуванням task)"`
	Priority     int             `json:"priority,omitempty" jsonschema:"пріоритет: 1 — звичайний (за замовчуванням), 2 — важливо, 3 — дуже важливо"`
	ScheduledAt  string          `json:"scheduled_at,omitempty" jsonschema:"термін (UTC) у форматі 2026-11-20 09:00:00; за замовчуванням зараз"`
	AssignedToID int             `json:"assigned_to_id" jsonschema:"відповідальний — активний користувач (list_users), обов'язково"`
	AssignedByID int             `json:"assigned_by_id" jsonschema:"постановник — активний користувач (list_users), обов'язково"`
	TaskedType   string          `json:"tasked_type,omitempty" enum:"order,lead,client" jsonschema:"прив'язка: order — замовлення, lead — картка воронки, client — покупець; лише разом з tasked_id"`
	TaskedID     int             `json:"tasked_id,omitempty" jsonschema:"ідентифікатор сутності прив'язки; лише разом з tasked_type"`
	ParentID     int             `json:"parent_id,omitempty" jsonschema:"батьківське завдання — створюється підзадача з його прив'язкою"`
	Settings     *Settings       `json:"settings,omitempty" jsonschema:"налаштування"`
	Checklist    []ChecklistItem `json:"checklist,omitempty" jsonschema:"чек-лист"`
}

// CreateTaskInput — вхід create_task.
type CreateTaskInput struct {
	TaskFields
}

// ImportTasksInput — вхід import_tasks.
type ImportTasksInput struct {
	Tasks   []TaskFields `json:"tasks" jsonschema:"завдання, до 50 за раз; поля — як у create_task"`
	Confirm bool         `json:"confirm,omitempty" jsonschema:"true — лише після того, як користувач побачив, що буде створено, і погодився"`
}

// UpdateTaskInput — вхід update_task.
type UpdateTaskInput struct {
	TaskID         int             `json:"task_id" jsonschema:"ідентифікатор завдання"`
	Name           *string         `json:"name,omitempty" jsonschema:"нова назва"`
	Description    *string         `json:"description,omitempty" jsonschema:"новий опис"`
	Type           string          `json:"type,omitempty" enum:"task,remind,call,chat,meeting,event" jsonschema:"новий тип"`
	Priority       int             `json:"priority,omitempty" jsonschema:"новий пріоритет: 1, 2 або 3"`
	ScheduledAt    string          `json:"scheduled_at,omitempty" jsonschema:"новий термін (UTC) у форматі 2026-11-20 09:00:00"`
	AssignedToID   int             `json:"assigned_to_id,omitempty" jsonschema:"новий відповідальний (list_users)"`
	TaskedType     string          `json:"tasked_type,omitempty" enum:"order,lead,client" jsonschema:"нова прив'язка; лише разом з tasked_id"`
	TaskedID       int             `json:"tasked_id,omitempty" jsonschema:"ідентифікатор сутності нової прив'язки; лише разом з tasked_type"`
	Unbind         bool            `json:"unbind,omitempty" jsonschema:"true — відв'язати завдання від сутності (не разом з tasked_type/tasked_id)"`
	Status         string          `json:"status,omitempty" enum:"open,done" jsonschema:"done — закрити (закриває й підзадачі), open — відновити"`
	ClosedByID     int             `json:"closed_by_id,omitempty" jsonschema:"хто закрив (list_users); лише разом зі status done"`
	Settings       *Settings       `json:"settings,omitempty" jsonschema:"налаштування"`
	Checklist      []ChecklistItem `json:"checklist,omitempty" jsonschema:"новий чек-лист: повністю замінює поточний, тож передавайте всі пункти"`
	ClearChecklist bool            `json:"clear_checklist,omitempty" jsonschema:"true — видалити чек-лист"`
}

// FileInput — вхід attach_task_file і detach_task_file.
type FileInput struct {
	TaskID int `json:"task_id" jsonschema:"ідентифікатор завдання"`
	FileID int `json:"file_id" jsonschema:"ідентифікатор файлу в сховищі keyCRM (upload_file, list_files)"`
}

// jsonContent — тип тіла запитів запису.
const jsonContent = "application/json"

func jsonBody(v any) (io.Reader, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return bytes.NewReader(b), nil
}

func validatePriority(p int, path string) error {
	if p != 0 && (p < 1 || p > 3) {
		return fmt.Errorf("%spriority must be 1, 2 or 3, got %d", path, p)
	}
	return nil
}

func validateTasked(taskedType string, taskedID int, path string) error {
	if (taskedType == "") != (taskedID == 0) {
		return fmt.Errorf("%spass tasked_type and tasked_id together", path)
	}
	return nil
}

func validateTask(t TaskFields, path string) error {
	if t.AssignedToID <= 0 || t.AssignedByID <= 0 {
		return fmt.Errorf("%sassigned_to_id and assigned_by_id are required (see list_users)", path)
	}
	if err := validatePriority(t.Priority, path); err != nil {
		return err
	}
	return validateTasked(t.TaskedType, t.TaskedID, path)
}

// updateBody перевіряє вхід update_task і будує тіло updateTask. Через map, бо
// відв'язування передається явними null, а очищення чек-листа — порожнім масивом.
func updateBody(in UpdateTaskInput) (map[string]any, error) {
	if err := validatePriority(in.Priority, ""); err != nil {
		return nil, err
	}
	if err := validateTasked(in.TaskedType, in.TaskedID, ""); err != nil {
		return nil, err
	}
	switch {
	case in.Unbind && in.TaskedType != "":
		return nil, errors.New("unbind cannot be combined with tasked_type/tasked_id")
	case in.ClearChecklist && len(in.Checklist) > 0:
		return nil, errors.New("clear_checklist cannot be combined with checklist")
	case in.ClosedByID != 0 && in.Status != "done":
		return nil, errors.New(`closed_by_id requires status "done"`)
	}
	b := map[string]any{}
	set := func(k string, ok bool, v any) {
		if ok {
			b[k] = v
		}
	}
	set("name", in.Name != nil, in.Name)
	set("description", in.Description != nil, in.Description)
	set("type", in.Type != "", in.Type)
	set("priority", in.Priority != 0, in.Priority)
	set("scheduled_at", in.ScheduledAt != "", in.ScheduledAt)
	set("assigned_to_id", in.AssignedToID != 0, in.AssignedToID)
	set("tasked_type", in.TaskedType != "", in.TaskedType)
	set("tasked_id", in.TaskedID != 0, in.TaskedID)
	if in.Unbind {
		b["tasked_type"], b["tasked_id"] = nil, nil
	}
	set("status", in.Status != "", in.Status)
	set("closed_by_id", in.ClosedByID != 0, in.ClosedByID)
	set("settings", in.Settings != nil, in.Settings)
	set("checklist", len(in.Checklist) > 0, in.Checklist)
	if in.ClearChecklist {
		b["checklist"] = []ChecklistItem{}
	}
	if len(b) == 0 {
		return nil, errors.New("nothing to update: pass at least one field besides task_id")
	}
	return b, nil
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_tasks",
		Description: "Список завдань keyCRM — компактно: id, назва, тип, статус, пріоритет, протерміноване, термін, відповідальний, прив'язка. " +
			"Повні дані (опис, чек-лист, підзадачі, файли) — get_task. Фільтри поєднуються. Приклади: " +
			`{"status": "open", "is_expired": true} — протерміновані; {"status": "open", "assigned_to_id": [12], "sort": "scheduled_at"}; ` +
			`{"tasked_type": "order", "tasked_id": [1024]} — завдання замовлення. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfTasks"},
	}, func(ctx context.Context, in ListTasksInput) (listing.Result[TaskSummary], error) {
		if len(in.TaskedID) > 0 && in.TaskedType == "" {
			return listing.Result[TaskSummary]{}, errors.New("tasked_id requires tasked_type")
		}
		for _, p := range in.Priority {
			if err := validatePriority(p, ""); err != nil {
				return listing.Result[TaskSummary]{}, err
			}
		}
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("id", in.ID)
		f.Set("status", in.Status)
		f.Set("is_expired", in.IsExpired)
		f.Set("type", in.Type)
		f.Set("priority", in.Priority)
		f.Set("assigned_to_id", in.AssignedToID)
		f.Set("assigned_by_id", in.AssignedByID)
		f.Set("closed_by_id", in.ClosedByID)
		f.Set("tasked_type", in.TaskedType)
		f.Set("tasked_id", in.TaskedID)
		f.Set("parent_id", in.ParentID)
		f.Set("is_subtask", in.IsSubtask)
		f.Set("scheduled_between", in.ScheduledBetween)
		f.Set("created_between", in.CreatedBetween)
		f.Set("updated_between", in.UpdatedBetween)
		f.Set("closed_between", in.ClosedBetween)
		params := &api.GetPaginatedListOfTasksParams{Page: &page, Limit: &limit, Filter: f.Param()}
		if in.Sort != "" {
			params.Sort = &in.Sort
		}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfTasks(ctx, params))
		if err != nil {
			return listing.Result[TaskSummary]{}, err
		}
		return listing.Decode[TaskSummary](status, body, page, note)
	})

	server.Add(r, server.ToolSpec{
		Name: "get_task",
		Description: "Повне завдання keyCRM за id: опис, чек-лист, прив'язка, підзадачі й файли. Приклади: " +
			`{"task_id": 4090}; {"task_id": 4090, "include": ["assigned_to", "parent"]}.`,
		Kind: server.KindRead, OperationIDs: []string{"getTaskById"},
	}, func(ctx context.Context, in GetTaskInput) (Object, error) {
		include := in.Include
		if len(include) == 0 {
			include = defaultInclude
		}
		return listing.Object(d.KeyCRM.API.GetTaskById(ctx, in.TaskID, &api.GetTaskByIdParams{Include: listing.Include(include)}))
	})

	server.Add(r, server.ToolSpec{
		Name: "create_task",
		Description: "Створює завдання в keyCRM. assigned_to_id і assigned_by_id обов'язкові (list_users); прив'язка до замовлення, картки воронки чи покупця — tasked_type разом з tasked_id. " +
			"Приклад: " + `{"name": "Передзвонити щодо оплати", "type": "call", "priority": 2, "scheduled_at": "2026-11-20 09:00:00", ` +
			`"assigned_to_id": 12, "assigned_by_id": 3, "tasked_type": "order", "tasked_id": 1024, "checklist": [{"value": "Перевірити виписку"}]}.`,
		Kind: server.KindWrite, OperationIDs: []string{"createTask"},
	}, func(ctx context.Context, in CreateTaskInput) (Object, error) {
		if err := validateTask(in.TaskFields, ""); err != nil {
			return nil, err
		}
		b, err := jsonBody(in.TaskFields)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.CreateTaskWithBody(ctx, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "update_task",
		Description: "Оновлює завдання: передавайте лише те, що змінюється. Закрити — status done (закриває й підзадачі), відновити — status open. " +
			"checklist повністю замінює поточний. Приклади: " +
			`{"task_id": 4090, "status": "done"}; {"task_id": 4090, "priority": 3, "scheduled_at": "2026-11-21 10:00:00", "assigned_to_id": 15}; ` +
			`{"task_id": 4090, "unbind": true}.`,
		Kind: server.KindIdempotent, OperationIDs: []string{"updateTask"},
	}, func(ctx context.Context, in UpdateTaskInput) (Object, error) {
		body, err := updateBody(in)
		if err != nil {
			return nil, err
		}
		b, err := jsonBody(body)
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.UpdateTaskWithBody(ctx, in.TaskID, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name: "import_tasks",
		Description: "Створює до 50 завдань одним запитом (поля кожного — як у create_task); якщо хоч одне не проходить перевірку, не створюється жодне. " +
			"Спершу покажіть користувачу перелік; викликайте з confirm: true лише після його згоди. Приклад: " +
			`{"tasks": [{"name": "Передзвонити", "assigned_to_id": 12, "assigned_by_id": 3}], "confirm": true}.`,
		Kind: server.KindWrite, OperationIDs: []string{"importTasks"},
	}, func(ctx context.Context, in ImportTasksInput) (Object, error) {
		if err := server.RequireConfirm(in.Confirm, "import_tasks"); err != nil {
			return nil, err
		}
		if len(in.Tasks) == 0 || len(in.Tasks) > maxImport {
			return nil, fmt.Errorf("tasks: pass 1 to %d tasks, got %d", maxImport, len(in.Tasks))
		}
		for i, t := range in.Tasks {
			if err := validateTask(t, fmt.Sprintf("tasks[%d].", i)); err != nil {
				return nil, err
			}
		}
		b, err := jsonBody(map[string]any{"tasks": in.Tasks})
		if err != nil {
			return nil, err
		}
		return listing.Object(d.KeyCRM.API.ImportTasksWithBody(ctx, nil, jsonContent, b))
	})

	server.Add(r, server.ToolSpec{
		Name:        "attach_task_file",
		Description: `Прикріплює до завдання файл, уже завантажений у сховище keyCRM (upload_file). Приклад: {"task_id": 4090, "file_id": 73609}.`,
		Kind:        server.KindIdempotent, OperationIDs: []string{"attachFileToTask"},
	}, func(ctx context.Context, in FileInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.AttachFileToTask(ctx, in.TaskID, in.FileID, nil))
	})

	server.Add(r, server.ToolSpec{
		Name:        "detach_task_file",
		Description: `Від'єднує файл від завдання; сам файл лишається у сховищі. Приклад: {"task_id": 4090, "file_id": 73609}.`,
		Kind:        server.KindDestructive, OperationIDs: []string{"detachFileFromTask"},
	}, func(ctx context.Context, in FileInput) (Object, error) {
		return listing.Object(d.KeyCRM.API.DetachFileFromTask(ctx, in.TaskID, in.FileID, nil))
	})
}
