package db

import (
	"encoding/json"
	"fmt"
	"time"
)

// Notification представляет запись в таблице notifications.
type Notification struct {
	ID       int64      `json:"id"`
	EventID  string     `json:"event_id"`
	Notifier string     `json:"notifier"`
	Status   string     `json:"status"`
	Created  time.Time  `json:"created_at"`
	SentAt   *time.Time `json:"sent_at,omitempty"`
	Error    string     `json:"error,omitempty"`
	Retries  int        `json:"retries"`
}

// CreateNotification сохраняет запись об отправке уведомления.
func (d *DB) CreateNotification(eventID, notifier string) (*Notification, error) {
	now := time.Now().Format(time.RFC3339Nano)

	var result int64
	err := d.db.QueryRow(
		`INSERT INTO notifications (event_id, notifier, status, created_at, retries)
		 VALUES (?, ?, 'pending', ?, 0)`,
		eventID, notifier, now,
	).Scan(&result)
	if err != nil {
		return nil, fmt.Errorf("создать запись уведомления: %w", err)
	}

	return &Notification{
		ID:       result,
		EventID:  eventID,
		Notifier: notifier,
		Status:   "pending",
		Created:  time.Now(),
		Retries:  0,
	}, nil
}

// UpdateNotificationStatus обновляет статус уведомления.
func (d *DB) UpdateNotificationStatus(id int64, status, errorStr string) error {
	now := time.Now().Format(time.RFC3339Nano)

	var sentAt *time.Time
	if status == "sent" {
		t := time.Now()
		sentAt = &t
	}

	_, err := d.db.Exec(
		`UPDATE notifications SET status = ?, sent_at = ?, error = ?, updated_at = ?
		 WHERE id = ?`,
		status, sentAt, errorStr, now, id,
	)
	if err != nil {
		return fmt.Errorf("обновить статус уведомления: %w", err)
	}

	return nil
}

// IncrementNotificationRetries увеличивает счётчик retries.
func (d *DB) IncrementNotificationRetries(id int64) error {
	_, err := d.db.Exec(
		`UPDATE notifications SET retries = retries + 1 WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("увеличить retries уведомления: %w", err)
	}
	return nil
}

// GetPendingNotifications возвращает уведомления со статусом 'pending'.
func (d *DB) GetPendingNotifications(limit int) ([]*Notification, error) {
	query := `SELECT id, event_id, notifier, status, created_at, sent_at, error, retries
	          FROM notifications WHERE status = 'pending'
	          ORDER BY created_at ASC LIMIT ?`

	if limit <= 0 {
		limit = 50
	}

	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*Notification, 0)
	for rows.Next() {
		var n Notification
		var createdAt, sentAtStr string
		var sentAt *time.Time

		err := rows.Scan(
			&n.ID, &n.EventID, &n.Notifier, &n.Status,
			&createdAt, &sentAtStr, &n.Error, &n.Retries,
		)
		if err != nil {
			continue
		}

		n.Created, _ = time.Parse(time.RFC3339Nano, createdAt)
		if sentAtStr != "" {
			t, _ := time.Parse(time.RFC3339Nano, sentAtStr)
			sentAt = &t
			n.SentAt = sentAt
		}

		result = append(result, &n)
	}

	return result, nil
}

// CameraState представляет запись в таблице camera_state.
type CameraState struct {
	CameraID          string    `json:"camera_id"`
	Name              string    `json:"name"`
	Status            string    `json:"status"`
	UptimeSeconds     int64     `json:"uptime_seconds"`
	LastSeen          time.Time `json:"last_seen"`
	ReconnectAttempts int       `json:"reconnect_attempts"`
	LastError         string    `json:"last_error"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// UpsertCameraState обновляет или создаёт запись состояния камеры.
func (d *DB) UpsertCameraState(cs *CameraState) error {
	cs.UpdatedAt = time.Now()
	lastSeen := cs.LastSeen.Format(time.RFC3339Nano)
	updatedAt := cs.UpdatedAt.Format(time.RFC3339Nano)

	_, err := d.db.Exec(
		`INSERT INTO camera_state (camera_id, name, status, uptime_seconds, last_seen, reconnect_attempts, last_error, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(camera_id) DO UPDATE SET
			name = excluded.name,
			status = excluded.status,
			uptime_seconds = excluded.uptime_seconds,
			last_seen = excluded.last_seen,
			reconnect_attempts = excluded.reconnect_attempts,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at`,
		cs.CameraID, cs.Name, cs.Status, cs.UptimeSeconds,
		lastSeen, cs.ReconnectAttempts, cs.LastError, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("обновить состояние камеры: %w", err)
	}

	return nil
}

// GetCameraState возвращает состояние камеры по ID.
func (d *DB) GetCameraState(cameraID string) (*CameraState, error) {
	var cs CameraState
	var lastSeen, updatedAt string

	err := d.db.QueryRow(
		`SELECT camera_id, name, status, uptime_seconds, last_seen, reconnect_attempts, last_error, updated_at
		 FROM camera_state WHERE camera_id = ?`,
		cameraID,
	).Scan(
		&cs.CameraID, &cs.Name, &cs.Status, &cs.UptimeSeconds,
		&lastSeen, &cs.ReconnectAttempts, &cs.LastError, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	cs.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen)
	cs.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)

	return &cs, nil
}

// GetAllCameraStates возвращает состояния всех камер.
func (d *DB) GetAllCameraStates() ([]*CameraState, error) {
	rows, err := d.db.Query(
		`SELECT camera_id, name, status, uptime_seconds, last_seen, reconnect_attempts, last_error, updated_at
		 FROM camera_state`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*CameraState, 0)
	for rows.Next() {
		var cs CameraState
		var lastSeen, updatedAt string

		err := rows.Scan(
			&cs.CameraID, &cs.Name, &cs.Status, &cs.UptimeSeconds,
			&lastSeen, &cs.ReconnectAttempts, &cs.LastError, &updatedAt,
		)
		if err != nil {
			continue
		}

		cs.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen)
		cs.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)

		result = append(result, &cs)
	}

	return result, nil
}

// EventWithMetadata — Event с полностью распарсенным metadata как map.
type EventWithMetadata struct {
	ID           string                 `json:"id"`
	CameraID     string                 `json:"camera_id"`
	Type         string                 `json:"type"`
	Source       string                 `json:"source"`
	StartedAt    time.Time              `json:"started_at"`
	EndedAt      *time.Time             `json:"ended_at,omitempty"`
	SnapshotPath string                 `json:"snapshot_path,omitempty"`
	VideoPath    string                 `json:"video_path,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// GetEventWithMetadata возвращает событие с полностью распарсенным JSON metadata.
func (d *DB) GetEventWithMetadata(eventID string) (*EventWithMetadata, error) {
	var e EventWithMetadata
	var startedAt, metaJSON string
	var endedAt *string

	err := d.db.QueryRow(
		`SELECT id, camera_id, type, source, started_at, ended_at, snapshot_path, video_path, metadata
		 FROM events WHERE id = ?`,
		eventID,
	).Scan(
		&e.ID, &e.CameraID, &e.Type, &e.Source,
		&startedAt, &endedAt, &e.SnapshotPath, &e.VideoPath, &metaJSON,
	)
	if err != nil {
		return nil, err
	}

	e.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	if endedAt != nil {
		t, _ := time.Parse(time.RFC3339Nano, *endedAt)
		e.EndedAt = &t
	}

	if metaJSON != "" && metaJSON != "{}" {
		json.Unmarshal([]byte(metaJSON), &e.Metadata)
	}

	return &e, nil
}

// ListEventsWithMetadata возвращает список событий с распарсенным metadata.
func (d *DB) ListEventsWithMetadata(cameraID, eventType string, from, to time.Time, limit int) ([]*EventWithMetadata, error) {
	query := `SELECT id, camera_id, type, source, started_at, ended_at, snapshot_path, video_path, metadata
	          FROM events WHERE 1=1`
	args := []interface{}{}

	if cameraID != "" {
		query += " AND camera_id = ?"
		args = append(args, cameraID)
	}
	if eventType != "" {
		query += " AND type = ?"
		args = append(args, eventType)
	}
	if !from.IsZero() {
		query += " AND started_at >= ?"
		args = append(args, from.Format(time.RFC3339Nano))
	}
	if !to.IsZero() {
		query += " AND started_at <= ?"
		args = append(args, to.Format(time.RFC3339Nano))
	}

	query += " ORDER BY started_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*EventWithMetadata, 0)
	for rows.Next() {
		var e EventWithMetadata
		var startedAt, metaJSON string
		var endedAt *string

		err := rows.Scan(
			&e.ID, &e.CameraID, &e.Type, &e.Source,
			&startedAt, &endedAt, &e.SnapshotPath, &e.VideoPath, &metaJSON,
		)
		if err != nil {
			continue
		}

		e.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
		if endedAt != nil {
			t, _ := time.Parse(time.RFC3339Nano, *endedAt)
			e.EndedAt = &t
		}

		if metaJSON != "" && metaJSON != "{}" {
			json.Unmarshal([]byte(metaJSON), &e.Metadata)
		}

		result = append(result, &e)
	}

	return result, nil
}
