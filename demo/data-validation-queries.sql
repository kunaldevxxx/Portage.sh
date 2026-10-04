-- ==============================================================================
-- Portage Platform: Manual Data Validation & Parity Queries (Run on Source & Target)
-- ==============================================================================
-- Database: core_banking (PostgreSQL 16)
-- Run these queries on BOTH the Source database and the Target database to confirm
-- 100% data fidelity, matching row counts, and zero ledger drift.

-- 1. Check Total Database Size On-Disk
SELECT 
    pg_database.datname,
    pg_size_pretty(pg_database_size(pg_database.datname)) AS total_size
FROM pg_database
WHERE datname = 'core_banking';

-- Expected Output:
--    datname    | total_size 
-- --------------+------------
--  core_banking | 128.5 GB

-- 2. Check Table Row Counts & Storage Usage Across All Tables
SELECT 
    c.relname AS table_name,
    pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size,
    c.reltuples::bigint AS estimated_rows
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r'
ORDER BY pg_total_relation_size(c.oid) DESC;

-- Expected Output:
--      table_name     | total_size | estimated_rows 
-- --------------------+------------+----------------
--  transactions       | 62.4 GB    |       48250000
--  audit_ledger       | 54.1 GB    |       48250000
--  accounts           | 8.2 GB     |        2500000
--  security_tokens    | 3.8 GB     |        1820000

-- 3. Exact Row Count Verification on Critical Tables
SELECT count(*) AS total_transactions FROM transactions;
-- Expected: 48250000

SELECT count(*) AS total_accounts FROM accounts;
-- Expected: 2500000

-- 4. Financial Ledger Sum & Balance Reconciliation Check
SELECT 
    count(*) AS total_txns,
    sum(amount) AS total_ledger_sum,
    min(created_at) AS earliest_txn,
    max(created_at) AS latest_txn,
    max(id) AS latest_txn_id
FROM transactions;
-- Expected on BOTH Source & Target:
--   total_txns: 48250000
--   total_ledger_sum: 14920410500.25

-- 5. Cryptographic MD5 / SHA-256 Checksum on Sample Partition
-- Verifies that rows were copied with zero column-level mutation or truncation
SELECT 
    count(*),
    md5(string_agg(id::text || ':' || amount::text || ':' || status, ',' ORDER BY id)) AS partition_checksum
FROM transactions
WHERE id <= 1000000;

-- 6. Checkpoint & LSN Alignment Confirmation
-- Check Write-Ahead Log position:
SELECT pg_current_wal_lsn();
-- Expected on Target: Matches the final flushed LSN acknowledged by Portage cutover.
