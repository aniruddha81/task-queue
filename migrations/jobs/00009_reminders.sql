-- Reminders booked on the live page, waiting for their address to be confirmed. Confirming one
-- submits it as an email.send job and deletes the row; an unconfirmed one is deleted after a
-- day. Rows hold email addresses, so nothing keeps them longer (the job keeps its address
-- until retention deletes it).

-- +goose Up
CREATE TABLE reminders (
  token      uuid PRIMARY KEY,               -- random (v4): the only proof of reading the email
  email      text NOT NULL,                  -- lower case
  label      text NOT NULL,
  remind_at  timestamptz NOT NULL,
  tz         text NOT NULL,                  -- the visitor's time zone, for the emails
  job_id     uuid NOT NULL DEFAULT uuidv7(), -- the job it becomes: confirming twice makes one
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reminders_email ON reminders (email);

-- +goose Down
DROP TABLE reminders;
