package knowledge

import "time"

type StatusEntry struct {
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	PageID       string     `json:"page_id"`
	URL          string     `json:"url"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	Complete     bool       `json:"content_complete"`
}

func Status(files Files) ([]StatusEntry, error) {
	bindings, err := files.Bindings()
	if err != nil {
		return nil, err
	}

	entries := make([]StatusEntry, 0, len(bindings))
	for index := range bindings {
		binding := &bindings[index]
		if binding.Provider != ProviderNotion || binding.ExternalId == nil {
			continue
		}

		entry := StatusEntry{PageID: *binding.ExternalId, Status: binding.SyncStatus, LastSyncedAt: binding.LastSyncedAt}
		if binding.ExternalUrl != nil {
			entry.URL = *binding.ExternalUrl
		}

		if binding.LastError != nil {
			entry.Error = *binding.LastError
		}

		page, err := files.LoadPage(entry.PageID)
		if err != nil {
			entry.Status = StatusError
			entry.Error = err.Error()
		} else {
			entry.Complete = page.ContentComplete != nil && *page.ContentComplete

			body, encodeErr := encodePrint(page)
			if encodeErr != nil {
				return nil, encodeErr
			}

			if entry.Status == StatusSynced && (binding.LastWrittenSha256 == nil || digest(body) != *binding.LastWrittenSha256) {
				entry.Status = StatusPending
			}
		}

		entries = append(entries, entry)
	}

	return entries, nil
}
