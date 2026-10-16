-- name: AcquireDeviceLease :one
INSERT INTO device_leases(device_id, holder, generation, expires_at)
VALUES ($1, $2, 1, now() + interval '45 seconds')
ON CONFLICT(device_id) DO UPDATE
SET holder = $2, generation = device_leases.generation + 1,
    expires_at = now() + interval '45 seconds'
WHERE device_leases.expires_at < now()
RETURNING generation;

-- name: RenewDeviceLease :execrows
UPDATE device_leases SET expires_at = now() + interval '45 seconds'
WHERE device_id = $1 AND holder = $2 AND generation = $3 AND expires_at > now();

-- name: ReleaseDeviceLease :exec
UPDATE device_leases SET expires_at = now()
WHERE device_id = $1 AND holder = $2 AND generation = $3;
