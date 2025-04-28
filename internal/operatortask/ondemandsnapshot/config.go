package ondemandsnapshot

type Config struct {
	SnapshotType   *string `json:"snapshotType,omitempty"`
	TimeoutSeconds *int32  `json:"timeoutSeconds,omitempty"`
}
