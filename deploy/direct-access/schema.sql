-- Run with the existing database administrator; application credentials are separate.
CREATE SCHEMA IF NOT EXISTS direct_access;
CREATE TABLE IF NOT EXISTS direct_access.entries (
    id uuid PRIMARY KEY,
    user_id bigint NOT NULL,
    ip inet NOT NULL CHECK (masklen(ip) IN (32,128)),
    label varchar(60) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS direct_access_owner_ip
    ON direct_access.entries(user_id,ip) WHERE deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS direct_access.events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL,
    action varchar(16) NOT NULL,
    entry_id uuid NOT NULL,
    ip inet NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS direct_access_events_rate ON direct_access.events(user_id,created_at);
CREATE TABLE IF NOT EXISTS direct_access.sync_state (
    id int PRIMARY KEY CHECK (id=1),
    ips text[] NOT NULL DEFAULT '{}',
    applied_at timestamptz NOT NULL DEFAULT '1970-01-01'
);
INSERT INTO direct_access.sync_state(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE OR REPLACE FUNCTION direct_access.ip_quota(recharged numeric)
RETURNS integer LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT least(100,floor(greatest(coalesce(recharged,0),0)/100))::integer
$$;
CREATE TABLE IF NOT EXISTS direct_access.quota_overrides (
    user_id bigint PRIMARY KEY,
    ip_limit integer CHECK (ip_limit BETWEEN 0 AND 100),
    note varchar(200) NOT NULL,
    updated_by bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS direct_access.quota_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id bigint NOT NULL,
    user_id bigint NOT NULL,
    old_limit integer,
    new_limit integer,
    note varchar(200) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS direct_access_quota_events_rate
    ON direct_access.quota_events(actor_id,created_at);
-- Both successful payment redemption and admin adjustments have a source
-- source record here. Do not add this sum to users.total_recharged: redeemed
-- balance is already represented in that legacy counter. Keep the larger total
-- for imported/older accounts with incomplete history, without lowering quotas.
CREATE OR REPLACE VIEW direct_access.recharge_totals AS
 SELECT used_by user_id,
        coalesce(sum(value) FILTER (WHERE type='balance'),0)::numeric redeem_credit_total,
        coalesce(sum(value) FILTER (WHERE type='admin_balance'),0)::numeric admin_credit_total,
        sum(value)::numeric history_credit_total
 FROM public.redeem_codes
 WHERE type IN ('balance','admin_balance') AND status='used' AND value>0
   AND used_by IS NOT NULL AND used_at IS NOT NULL
 GROUP BY used_by;
CREATE OR REPLACE VIEW direct_access.entitlements AS
 SELECT u.id user_id,u.effective_recharged total_recharged,
        coalesce(q.ip_limit,direct_access.ip_quota(u.effective_recharged)) ip_limit,
        direct_access.ip_quota(u.effective_recharged) auto_limit,
        q.ip_limit manual_limit,u.system_total_recharged,u.redeem_credit_total,u.admin_credit_total
 FROM (
   SELECT u.id,coalesce(u.total_recharged,0)::numeric system_total_recharged,
          coalesce(r.redeem_credit_total,0)::numeric redeem_credit_total,
          coalesce(r.admin_credit_total,0)::numeric admin_credit_total,
          greatest(coalesce(u.total_recharged,0),coalesce(r.history_credit_total,0),0)::numeric effective_recharged
   FROM public.users u LEFT JOIN direct_access.recharge_totals r ON r.user_id=u.id
   WHERE u.deleted_at IS NULL
 ) u LEFT JOIN direct_access.quota_overrides q ON q.user_id=u.id;
CREATE OR REPLACE VIEW direct_access.quota_admin_users AS
 SELECT u.id user_id,u.email,u.username,u.status,e.total_recharged,
        e.ip_limit,e.auto_limit,e.manual_limit,q.note,q.updated_at,
        e.system_total_recharged,e.redeem_credit_total,e.admin_credit_total
 FROM public.users u JOIN direct_access.entitlements e ON e.user_id=u.id
 LEFT JOIN direct_access.quota_overrides q ON q.user_id=u.id
 WHERE u.deleted_at IS NULL;
CREATE OR REPLACE VIEW direct_access.eligible_entries AS
 SELECT id,user_id,ip,label,created_at,deleted_at FROM (
   SELECT e.*,q.ip_limit,row_number() OVER (PARTITION BY e.user_id ORDER BY e.created_at,e.id) position
   FROM direct_access.entries e JOIN public.users u ON u.id=e.user_id
   JOIN direct_access.entitlements q ON q.user_id=e.user_id
   WHERE e.deleted_at IS NULL AND u.deleted_at IS NULL AND u.status='active'
 ) ranked WHERE position<=ip_limit;
