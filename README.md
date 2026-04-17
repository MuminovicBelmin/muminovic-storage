# Muminovic Cloud

This project is a fully separate Nextcloud stack for `storage.muminovic.devotus.eu`.

## Isolation

- Separate compose project name: `muminovic_cloud`
- Separate containers: `muminovic_cloud_app`, `muminovic_cloud_cron`, `muminovic_cloud_db`, `muminovic_cloud_valkey`
- Separate Docker network: `muminovic_cloud_backend`
- Separate Docker volumes: `muminovic_cloud_mariadb_data`, `muminovic_cloud_nextcloud_html`, `muminovic_cloud_nextcloud_tmp`
- Separate host binding: Nextcloud only listens on `127.0.0.1:17880`
- Separate nginx vhost and separate certificate path for `storage.muminovic.devotus.eu`
- Separate database, cache, object store bucket, and secrets files

Nothing in this directory reuses the existing `storage.demoncalendar.online` host, its database, its cache, its certificate paths, or its Docker volumes.

## Object Storage

- Bucket: `muminovic-storage`
- Access key and secret key were taken from `user-APPayrJrGP8A.txt` and stored in local secret files
- The compose file uses Nextcloud's S3 primary object storage support
- `OBJECTSTORE_S3_AUTOCREATE` is set to `false` so the stack fails fast instead of silently creating the wrong bucket

The only OVH value that was not present in the credential file is the bucket region. `.env` is prefilled for OVH Gravelines (`gra`) because that is the only regional hint available from the reference setup. If the bucket was created in another OVH region, change both `OVH_S3_REGION` and `OVH_S3_HOST` in `.env` before the first start.

## Cache Choice

This stack uses Valkey. Nextcloud itself still exposes Redis-compatible configuration knobs (`REDIS_HOST`, `memcache.distributed`, `memcache.locking`), and the official image uses the `phpredis` extension. Valkey is used here as a Redis-protocol-compatible backend.

If you want the officially documented choice instead, replace `${VALKEY_IMAGE}` in `.env` with `redis:7-alpine` and keep the rest of the compose file unchanged.

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

Do not touch the existing `demoncalendar.online` certificate. Issue a separate Let's Encrypt certificate for `storage.muminovic.devotus.eu` and keep it in its own live path:

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

## What Was Done

This is the short version of the actual deployment work performed on the server:

1. Inspected the existing host first: nginx layout, Docker containers, open ports, DNS resolution, and existing certificate paths.
2. Uploaded this project to the server with `scp` instead of rewriting files through the shell.
3. Replaced the broken first-attempt nginx config with the bootstrap host config so nginx could reload without requiring a certificate that did not exist yet.
4. Verified ACME over plain HTTP with a test file under `/var/www/certbot/.well-known/acme-challenge/`.
5. Issued a separate Let's Encrypt certificate for `storage.muminovic.devotus.eu` with Certbot webroot mode.
6. Started the new isolated Compose stack and verified it came up on `127.0.0.1:17880`.
7. Fixed the first startup issue: `90-muminovic.config.php` was originally mounted into `/var/www/html/config`, which left that directory root-owned during first install. The mount was moved to `/usr/src/nextcloud/config`.
8. Fixed the second startup issue: the dedicated temp volume was not writable by `www-data` on a clean deploy. A one-shot `tmp_init` service was added to set ownership to `33:33` before `app` and `cron` start.
9. Recreated only the new stack's own containers and volumes after those fixes, then confirmed Nextcloud finished installation and returned `200 OK` on local `status.php`.
10. Switched nginx to the final TLS config, tested it with `nginx -t`, reloaded nginx, and verified `https://storage.muminovic.devotus.eu/status.php`.
11. Rechecked the existing `storage.demoncalendar.online` and `demoncalendar.online` endpoints to confirm they still responded after the nginx cutover.

Note: strict `curl` verification on the old demoncalendar certificate chain still reports `unable to get local issuer certificate`. That was pre-existing and was not introduced by this deployment.