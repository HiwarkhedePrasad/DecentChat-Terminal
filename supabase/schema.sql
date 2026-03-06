-- Supabase SQL Schema for DecentChat
-- Run this in your Supabase SQL Editor

-- Enable UUID extension if not already enabled
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Users table for signaling and presence
CREATE TABLE IF NOT EXISTS users (
    user_id TEXT PRIMARY KEY,
    public_identity_key TEXT NOT NULL,
    public_enc_key TEXT NOT NULL,
    webrtc_offer TEXT DEFAULT '',
    webrtc_answer TEXT DEFAULT '',
    online_status BOOLEAN DEFAULT false,
    last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for faster online user queries
CREATE INDEX IF NOT EXISTS idx_users_online ON users(online_status) WHERE online_status = true;
CREATE INDEX IF NOT EXISTS idx_users_last_seen ON users(last_seen);

-- Enable Row Level Security
ALTER TABLE users ENABLE ROW LEVEL SECURITY;

-- Policy: Allow anyone to read users (for discovery)
CREATE POLICY "Allow public read access" ON users
    FOR SELECT
    USING (true);

-- Policy: Allow anyone to insert/update (for signaling)
CREATE POLICY "Allow public write access" ON users
    FOR ALL
    USING (true);

-- Function to clean up stale sessions (older than 2 minutes)
CREATE OR REPLACE FUNCTION cleanup_stale_sessions()
RETURNS void AS $$
BEGIN
    UPDATE users
    SET 
        online_status = false,
        webrtc_offer = '',
        webrtc_answer = ''
    WHERE 
        online_status = true 
        AND last_seen < NOW() - INTERVAL '2 minutes';
END;
$$ LANGUAGE plpgsql;

-- Create a cron job to run cleanup every minute
-- Note: You need to enable pg_cron extension in Supabase
-- SELECT cron.schedule('cleanup-stale-sessions', '* * * * *', 'SELECT cleanup_stale_sessions()');

-- Alternative: Create a trigger on last_seen update
CREATE OR REPLACE FUNCTION update_last_seen()
RETURNS TRIGGER AS $$
BEGIN
    NEW.last_seen = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_last_seen
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION update_last_seen();
