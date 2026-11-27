package httpapi

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/settings"
)

// ListSettings returns the settings of a node with their effective values.
func (h *CoreHandlers) ListSettings(ctx context.Context, req gen.ListSettingsRequestObject) (gen.ListSettingsResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.GetSettings.Execute(ctx, settings.GetSettingsInput{Actor: a, NodeID: nodeID(req.Id)})
	if err != nil {
		return nil, err
	}
	items := make([]gen.SettingItem, len(out.Items))
	for i, it := range out.Items {
		if items[i], err = settingItemDTO(it); err != nil {
			return nil, err
		}
	}
	return gen.ListSettings200JSONResponse{Body: gen.SettingList{Items: items}}, nil
}

// PutSetting sets the override of one key on a node.
func (h *CoreHandlers) PutSetting(ctx context.Context, req gen.PutSettingRequestObject) (gen.PutSettingResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	out, err := h.d.SetSetting.Execute(ctx, settings.SetSettingInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id), Key: req.Key, Value: rawValue(req.Body.Value),
	})
	if err != nil {
		return nil, err
	}
	item, err := settingItemDTO(out.Item)
	if err != nil {
		return nil, err
	}
	return gen.PutSetting200JSONResponse{Body: item}, nil
}

// DeleteSetting removes the override of one key on a node.
func (h *CoreHandlers) DeleteSetting(ctx context.Context, req gen.DeleteSettingRequestObject) (gen.DeleteSettingResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.UnsetSetting.Execute(ctx, settings.UnsetSettingInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id), Key: req.Key,
	})
	if err != nil {
		return nil, err
	}
	item, err := settingItemDTO(out.Item)
	if err != nil {
		return nil, err
	}
	return gen.DeleteSetting200JSONResponse{Body: item}, nil
}
