-- One row per span, data point, or log record the worker received or emitted. Attributes hold the
-- resource's and scope's under the record's own. Label is a token type, which the lane summary sums by.
CREATE TABLE telemetry_records (
  id            INTEGER PRIMARY KEY,
  received_at   INTEGER NOT NULL,
  signal        TEXT    NOT NULL,
  name          TEXT    NOT NULL,
  lane_id       TEXT    NOT NULL,
  work_order_id TEXT    NOT NULL,
  generation    INTEGER NOT NULL,
  observed_at   INTEGER NOT NULL,
  value         REAL,
  label         TEXT    NOT NULL,
  attributes    TEXT    NOT NULL,
  detail        TEXT    NOT NULL
);

CREATE INDEX telemetry_received ON telemetry_records (received_at);
CREATE INDEX telemetry_lane_name ON telemetry_records (lane_id, name, observed_at);
CREATE INDEX telemetry_name ON telemetry_records (name, observed_at);
