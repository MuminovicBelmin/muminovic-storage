# Muminovic Cloud

This project is a fully separate Nextcloud stack for `storage.muminovic.devotus.eu`.

## Updates

Use the project-specific updater documented in [UPDATE.md](UPDATE.md). It is adapted
for this stack's `app`, `db`, `cron`, `valkey`, and `tmp_init` services and S3
primary storage; do not use the updater from the separate legacy deployment.

## Isolation

- Separate compose project name: `muminovic_cloud`
- Separate containers: `muminovic_cloud_app`, `muminovic_cloud_cron`, `muminovic_cloud_db`, `muminovic_cloud_valkey`, `muminovic_cloud_tmp_init`
- Separate Docker network: `muminovic_cloud_backend`
- Separate Docker volumes: `muminovic_cloud_mariadb_data`, `muminovic_cloud_nextcloud_html`, `muminovic_cloud_nextcloud_tmp`
- Separate host binding: Nextcloud only listens on `127.0.0.1:17880`
- Separate nginx vhost and separate certificate path for `storage.muminovic.devotus.eu`
- Separate database, cache, object store bucket, and secrets files

The Compose configuration uses project-specific container, network, and volume names.

## Object Storage

- Bucket: `muminovic-storage`
- Access key and secret key are supplied through local secret files
- The compose file uses Nextcloud's S3 primary object storage support
- `OBJECTSTORE_S3_AUTOCREATE` is set to `false` so the stack fails fast instead of silently creating the wrong bucket

`.env` currently targets OVH Gravelines (`gra`). Before the first start, set both `OVH_S3_REGION` and `OVH_S3_HOST` to the actual region and endpoint of the bucket.

## Cache Choice

This stack uses Valkey. Nextcloud itself still exposes Redis-compatible configuration knobs (`REDIS_HOST`, `memcache.distributed`, `memcache.locking`), and the official image uses the `phpredis` extension. Valkey is used here as a Redis-protocol-compatible backend.

To use Redis instead, change `${VALKEY_IMAGE}` and update the Valkey service command and health check to use Redis equivalents (`redis-server` and `redis-cli`). The current Compose file invokes `valkey-server` and `valkey-cli` directly.

## Reverse Proxy and Maintenance

The app and cron services trust proxy requests from `127.0.0.1` and `172.18.0.1` and use `HTTP_X_FORWARDED_FOR` for forwarded client addresses. If the host proxy or Docker gateway address changes, update `TRUSTED_PROXIES` in both services before deployment.

The bundled Nextcloud configuration schedules maintenance work from 01:00 UTC with `maintenance_window_start`.

## Large Files

This stack is tuned for multi-hundred-GB uploads:

- nginx sets `client_max_body_size 0`
- nginx disables request buffering and temp-file caps on the frontend proxy
- host nginx client-body temp storage must stay on a filesystem sized for worst-case concurrent uploads
- Apache request body limit is set to unlimited
- PHP upload limit is raised to `1024G`
- PHP and proxy timeouts are raised to 24 hours
- Nextcloud temp space is moved to its own dedicated Docker volume
- A one-shot `tmp_init` service sets the temp volume ownership to `www-data` on clean deploys
- Redis-compatible distributed cache is enabled, which is required for efficient S3 multipart upload behavior on recent Nextcloud versions

This is not a 16 GB design. The remaining practical limits are your network stability, browser or client behavior, timeout budget, and the amount of temporary space you want to reserve for concurrent uploads.

I did not hard-code `client_body_temp_path` because that must match the actual host filesystem you want nginx to use for spillover request buffering. Pick that path on the server itself if the default nginx temp location is too small.

## HTTPS Strategy

Issue a dedicated Let's Encrypt certificate for `storage.muminovic.devotus.eu` and keep it in its own live path:

- `/etc/letsencrypt/live/storage.muminovic.devotus.eu/fullchain.pem`
- `/etc/letsencrypt/live/storage.muminovic.devotus.eu/privkey.pem`

The supplied `nginx.conf` is the final host-nginx fragment for normal operation. It assumes the certificate files already exist.

If you load the final file before cert issuance, nginx will fail with an error like:

```text
cannot load certificate "/etc/letsencrypt/live/storage.muminovic.devotus.eu/fullchain.pem": No such file or directory
```

Use the included `nginx-bootstrap.conf` first, obtain the certificate, and only then switch to `nginx.conf`.

Bootstrap flow on Ubuntu:

```bash
sudo mkdir -p /var/www/certbot
sudo cp /path/to/muminovic_cloud/nginx-bootstrap.conf /etc/nginx/sites-available/storage.muminovic.devotus.eu.conf
sudo ln -s /etc/nginx/sites-available/storage.muminovic.devotus.eu.conf /etc/nginx/sites-enabled/storage.muminovic.devotus.eu.conf
sudo nginx -t
sudo systemctl reload nginx
sudo certbot certonly --webroot -w /var/www/certbot -d storage.muminovic.devotus.eu
sudo cp /path/to/muminovic_cloud/nginx.conf /etc/nginx/sites-available/storage.muminovic.devotus.eu.conf
sudo nginx -t
sudo systemctl reload nginx
```

After the certificate exists, renewals can keep using the normal final config because the ACME challenge location is present there too.

## Deploy Order

1. Point `storage.muminovic.devotus.eu` to the server.
2. Verify the OVH bucket region in `.env`.
3. Load `nginx-bootstrap.conf` on the Ubuntu host and reload nginx.
4. Issue the separate Let's Encrypt certificate for the new hostname.
5. Replace the bootstrap file with `nginx.conf`, test, and reload nginx.
6. Start the stack with `docker compose up -d` from this directory.
7. Check merged runtime config with `docker compose exec -u 33 app php occ config:list system`.
