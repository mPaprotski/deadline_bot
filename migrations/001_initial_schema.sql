-- 001_initial_schema.sql
-- Initial database schema for Deadline Bot

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    invite_code TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY, -- Telegram User ID
    group_id INTEGER REFERENCES groups(id) ON DELETE SET NULL,
    username TEXT NOT NULL DEFAULT '',
    first_name TEXT NOT NULL DEFAULT '',
    last_name TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'student' CHECK(role IN ('student', 'admin', 'owner')),
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'revoked')),
    is_blocked INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_settings (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    remind_all INTEGER NOT NULL DEFAULT 1,
    remind_3d INTEGER NOT NULL DEFAULT 1,
    remind_1d INTEGER NOT NULL DEFAULT 1,
    remind_3h INTEGER NOT NULL DEFAULT 1,
    notify_new_lab INTEGER NOT NULL DEFAULT 1,
    notify_changes INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS subjects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    is_archived INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(group_id, name)
);

CREATE TABLE IF NOT EXISTS labs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    subject_id INTEGER NOT NULL REFERENCES subjects(id) ON DELETE RESTRICT,
    number TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    submission_url TEXT NOT NULL DEFAULT '',
    file_id TEXT NOT NULL DEFAULT '',
    submission_method TEXT NOT NULL DEFAULT '',
    teacher_comment TEXT NOT NULL DEFAULT '',
    deadline_at INTEGER NOT NULL, -- Unix timestamp (UTC)
    deadline_version INTEGER NOT NULL DEFAULT 1,
    is_cancelled INTEGER NOT NULL DEFAULT 0,
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS lab_deadline_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    lab_id INTEGER NOT NULL REFERENCES labs(id) ON DELETE CASCADE,
    old_deadline_at INTEGER NOT NULL,
    new_deadline_at INTEGER NOT NULL,
    version_from INTEGER NOT NULL,
    version_to INTEGER NOT NULL,
    changed_by INTEGER NOT NULL REFERENCES users(id),
    changed_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS student_submissions (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lab_id INTEGER NOT NULL REFERENCES labs(id) ON DELETE CASCADE,
    marked_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, lab_id)
);

CREATE TABLE IF NOT EXISTS notification_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lab_id INTEGER NOT NULL REFERENCES labs(id) ON DELETE CASCADE,
    deadline_version INTEGER NOT NULL DEFAULT 1,
    notification_type TEXT NOT NULL CHECK(notification_type IN ('3d', '1d', '3h', 'overdue', 'new_lab', 'lab_changed', 'lab_cancelled')),
    scheduled_at INTEGER NOT NULL, -- Unix timestamp (UTC)
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'processing', 'sent', 'cancelled', 'failed')),
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    sent_at INTEGER,
    created_at INTEGER NOT NULL,
    UNIQUE(user_id, lab_id, deadline_version, notification_type)
);

CREATE TABLE IF NOT EXISTS dialogue_states (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    step TEXT NOT NULL,
    draft_data TEXT NOT NULL, -- JSON
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    action TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_users_group_status ON users(group_id, status);
CREATE INDEX IF NOT EXISTS idx_subjects_group_archived ON subjects(group_id, is_archived);
CREATE INDEX IF NOT EXISTS idx_labs_group_deadline ON labs(group_id, is_cancelled, deadline_at);
CREATE INDEX IF NOT EXISTS idx_labs_subject ON labs(subject_id);
CREATE INDEX IF NOT EXISTS idx_notification_jobs_status_scheduled ON notification_jobs(status, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_notification_jobs_user_lab ON notification_jobs(user_id, lab_id);
CREATE INDEX IF NOT EXISTS idx_student_submissions_lab ON student_submissions(lab_id);
