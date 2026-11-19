// SPDX-License-Identifier: AGPL-3.0-only

// Package files — сховище файлів keyCRM: завантаження за посиланням чи з
// локального каталогу, список файлів і файли окремої сутності.
package files

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група files.
var Toolset = server.Toolset{Name: "files", Register: register}

// Object — об'єкт keyCRM як його повертає API.
type Object = map[string]any

// UploadInput — вхід upload_file.
type UploadInput struct {
	URL      string `json:"url,omitempty" jsonschema:"публічне http(s)-посилання на файл; або file_path"`
	FilePath string `json:"file_path,omitempty" jsonschema:"шлях до файлу всередині каталогу KEYCRM_UPLOAD_DIR (відносний — від нього); працює, лише якщо каталог налаштовано"`
	Filename string `json:"filename,omitempty" jsonschema:"ім'я файлу в keyCRM, напр. invoice-1024.pdf; за замовчуванням — з посилання чи шляху"`
}

// ListFilesInput — вхід list_files.
type ListFilesInput struct {
	listing.Page
	Sort string `json:"sort,omitempty" enum:"size,-size" jsonschema:"сортування за розміром: size — від менших, -size — від більших"`
}

// EntityFilesInput — вхід list_entity_files.
type EntityFilesInput struct {
	EntityType string `json:"entity_type" enum:"order,pipelines,tasks" jsonschema:"тип сутності: order — замовлення, pipelines — картка у воронці, tasks — завдання"`
	EntityID   int    `json:"entity_id" jsonschema:"ідентифікатор сутності"`
}

// EntityFilesResult — результат list_entity_files (не пагінований).
type EntityFilesResult struct {
	Items []Object `json:"items"`
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "upload_file",
		Description: "Завантажує файл у сховище keyCRM і повертає його id — далі його можна прикріпити до замовлення (attach_order_file) чи завдання (attach_task_file). " +
			"Передайте рівно одне: url (публічне http(s)-посилання; приватні й локальні адреси заборонені) або file_path (лише всередині KEYCRM_UPLOAD_DIR). " +
			"Ліміт — 50 МБ. Приклади: " + `{"url": "https://example.com/invoice.pdf"}; {"file_path": "invoices/1024.pdf", "filename": "Рахунок 1024.pdf"}.`,
		Kind: server.KindWrite, OperationIDs: []string{"uploadStorageFile"},
	}, func(ctx context.Context, in UploadInput) (Object, error) {
		rawURL, path := strings.TrimSpace(in.URL), strings.TrimSpace(in.FilePath)
		if (rawURL == "") == (path == "") {
			return nil, errors.New("pass exactly one of url or file_path")
		}
		upload := d.KeyCRM.UploadFromURL
		src := rawURL
		if path != "" {
			upload, src = d.KeyCRM.UploadLocal, path
		}
		raw, err := upload(ctx, d.Upload, src, in.Filename)
		if err != nil {
			return nil, err
		}
		return listing.DecodeObject(http.StatusOK, raw)
	})

	server.Add(r, server.ToolSpec{
		Name: "list_files",
		Description: "Список файлів у сховищі keyCRM: id, оригінальна назва, розмір, тип, посилання, дата. Приклади: " +
			`{"limit": 10}; {"sort": "-size"} — спершу найбільші.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfFiles"},
	}, func(ctx context.Context, in ListFilesInput) (listing.Result[Object], error) {
		page, limit, note := in.Normalize()
		params := &api.GetPaginatedListOfFilesParams{Page: &page, Limit: &limit}
		if in.Sort != "" {
			params.Sort = &in.Sort
		}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfFiles(ctx, params))
		if err != nil {
			return listing.Result[Object]{}, err
		}
		return listing.Decode[Object](status, body, page, note)
	})

	server.Add(r, server.ToolSpec{
		Name: "list_entity_files",
		Description: "Файли, прикріплені до замовлення, картки воронки чи завдання. Приклади: " +
			`{"entity_type": "order", "entity_id": 1024}; {"entity_type": "tasks", "entity_id": 4090}.`,
		Kind: server.KindRead, OperationIDs: []string{"getEntityListOfFiles"},
	}, func(ctx context.Context, in EntityFilesInput) (EntityFilesResult, error) {
		status, body, err := listing.Raw(d.KeyCRM.API.GetEntityListOfFiles(ctx, in.EntityType, strconv.Itoa(in.EntityID)))
		if err != nil {
			return EntityFilesResult{}, err
		}
		obj, err := listing.DecodeObject(status, body)
		if err != nil {
			return EntityFilesResult{}, err
		}
		return EntityFilesResult{Items: objects(obj["data"])}, nil
	})
}

// objects перетворює масив з відповіді keyCRM на список об'єктів; не-об'єкти
// пропускаються, відсутній масив — порожній список.
func objects(v any) []Object {
	arr, _ := v.([]any)
	out := make([]Object, 0, len(arr))
	for _, it := range arr {
		if obj, ok := it.(Object); ok {
			out = append(out, obj)
		}
	}
	return out
}
