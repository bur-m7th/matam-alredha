# Deploying to Portainer with Cloudflare

This walks through putting the system on your own server, published on your
domain through Cloudflare, with no ports open to the internet.

**What you need:** a server (a small VPS or a machine at the ma'tam), Docker and
Portainer already installed on it, SSH access to that server, and your domain
already added to your Cloudflare account.

Throughout, replace `matam.example.org` with the hostname you actually want and
`/opt/matam` with wherever you prefer to keep the source.

---

## Why a tunnel rather than an open port

The setup below uses a **Cloudflare Tunnel**. A small container on your server
dials *out* to Cloudflare and keeps that connection open; Cloudflare sends
visitors down it. Nothing listens on a public port.

That matters here for three practical reasons:

- No port forwarding on the router, so this works on a home or office
  connection, and on an ISP that hands out shared addresses.
- Your server's real address is never published.
- HTTPS is handled by Cloudflare, so there is no certificate to install or renew.

If you would rather run your own reverse proxy with your own certificate, skip
to [Alternative: your own reverse proxy](#alternative-your-own-reverse-proxy).

---

## Step 1 — Put the source on the server

From your own computer:

```bash
scp matam-alredha.tar.gz youruser@your-server:/tmp/
```

Then on the server:

```bash
ssh youruser@your-server
sudo mkdir -p /opt/matam
sudo tar xzf /tmp/matam-alredha.tar.gz -C /opt/matam --strip-components=1
cd /opt/matam
ls        # you should see Dockerfile, docker-compose.yml, internal/, web/
```

---

## Step 2 — Build the image

Portainer's stack editor has no build context of its own, so build the image
once here. This takes a few minutes the first time.

```bash
cd /opt/matam
sudo docker build -t matam-alredha:1.0 .
```

Confirm it exists:

```bash
sudo docker images | grep matam-alredha
```

> **When you update the project later,** replace the source, then build with a
> new tag — `matam-alredha:1.1` — and change the tag in the Portainer stack.
> Using a new tag each time makes it obvious which version is running and lets
> you roll back by pointing at the old tag.

---

## Step 3 — Create the Cloudflare tunnel

1. Open the [Cloudflare dashboard](https://dash.cloudflare.com) and pick your
   domain.
2. In the left sidebar go to **Zero Trust**, then **Networks → Tunnels**.
3. **Create a tunnel** → choose **Cloudflared** → give it a name such as
   `matam` → **Save**.
4. Cloudflare shows an install command containing a long token. **Copy just the
   token** — the long string after `--token`. Do not run the command; the
   container will use the token instead.
5. On the **Route Traffic** step (also reachable later under **Public
   Hostnames**), add:

   | Field | Value |
   |---|---|
   | Subdomain | `matam` (or leave blank for the root domain) |
   | Domain | `example.org` |
   | Type | `HTTP` |
   | URL | `matam:8080` |

   `matam` there is the container name, not a domain. The tunnel container and
   the application share a Docker network, so it resolves by name.

   **Type is HTTP, not HTTPS.** The hop from the tunnel container to the
   application happens inside Docker. Cloudflare still serves your visitors over
   HTTPS.

6. **Save**. Cloudflare creates the DNS record for you — there is nothing to add
   by hand.

---

## Step 4 — Deploy the stack in Portainer

1. Open Portainer → **Stacks** → **Add stack**.
2. Name it `matam`.
3. Choose **Web editor** and paste the contents of
   `docker-compose.portainer.yml` from the project.
4. Scroll to **Environment variables** and add these, using **Advanced mode** to
   paste them all at once:

```
ADMIN_USERNAME=admin
ADMIN_PASSWORD=choose-a-strong-password-here
ELECTIONS_USERNAME=elections
ELECTIONS_PASSWORD=choose-a-different-one-here
ADMIN_PATH=pick-your-own-secret-path
ELECTIONS_PATH=pick-another-secret-path
CLOUDFLARE_TUNNEL_TOKEN=paste-the-token-from-step-3
```

5. **Deploy the stack.**

Two containers should come up: `matam-alredha` and `matam-tunnel`. Within a
minute the application container shows **healthy**.

> **Set the passwords and paths before the first deploy.** The two accounts are
> created on the very first start and are not recreated afterwards, so changing
> these variables later has no effect. Recovery is documented in the README
> under استعادة الدخول.

---

## Step 5 — Check it

Visit `https://matam.example.org`. You should get the member login page.

Then check your dashboards, at whatever you set `ADMIN_PATH` and
`ELECTIONS_PATH` to:

```
https://matam.example.org/your-secret-admin-path
https://matam.example.org/your-secret-elections-path
```

You can also reach them without the URL: press and hold the small brass diamond
at the foot of any page on a phone, or click it five times on a computer.

**Confirm the import worked.** In Portainer open the `matam-alredha` container →
**Logs**. You should see a line reporting that 378 existing members were
imported. That happens only on the very first start.

---

## Step 6 — Cloudflare settings worth adjusting

In the dashboard for your domain:

**SSL/TLS → Overview** — set encryption mode to **Full**. With a tunnel this is
handled for you, but check it is not on *Flexible*.

**SSL/TLS → Edge Certificates** — turn on **Always Use HTTPS**. The session
cookies are marked `Secure`, so a visitor who lands on `http://` would not stay
logged in.

**Speed → Optimization** — leave Rocket Loader **off**. It reorders JavaScript
and can break the pages.

**Caching → Configuration** — the default is fine. The application already sends
its own caching instructions: photographs are cached for a year, and pages and
API responses are not cached at all.

### Optional: shield the dashboards further

Zero Trust can require a second login before the dashboard is even reachable.
Under **Zero Trust → Access → Applications**, add a self-hosted application for
`matam.example.org/your-secret-admin-path` and add a policy allowing only your
own email addresses. Cloudflare then emails a one-time code before anyone sees
the page.

This is worth doing. The secret path stops members stumbling in; it does not
stop someone determined.

---

## Backups

Everything that matters lives in the `matam-data` volume: the database, the
uploaded photographs, the print template, and the generated spreadsheet.

In Portainer: **Volumes → matam-data** shows where it sits on disk. To take a
copy over SSH:

```bash
sudo docker run --rm \
  -v matam-data:/data \
  -v /opt/backups:/backup \
  debian:bookworm-slim \
  tar czf /backup/matam-$(date +%F).tar.gz -C /data .
```

Run that nightly with cron. To restore, stop the stack, then:

```bash
sudo docker run --rm \
  -v matam-data:/data \
  -v /opt/backups:/backup \
  debian:bookworm-slim \
  sh -c "rm -rf /data/* && tar xzf /backup/matam-2026-08-01.tar.gz -C /data"
```

Downloading the Excel file from the dashboard is a useful habit, but it is not a
backup: it holds the members and nothing else — no candidacies, no photographs,
no votes. Take a full copy of the volume before and after election day.

---

## Updating later

```bash
cd /opt/matam
sudo tar xzf /tmp/matam-alredha-new.tar.gz -C /opt/matam --strip-components=1
sudo docker build -t matam-alredha:1.1 .
```

Then in Portainer: **Stacks → matam → Editor**, change the image tag to `1.1`,
and **Update the stack**.

Your data is untouched — it lives in the volume, not the image. The application
adds any new database columns itself on startup.

---

## Troubleshooting

**The site shows a Cloudflare error 502 or 1033.**
The tunnel cannot reach the application. Check the public hostname points at
`matam:8080` with type `HTTP`, and that both containers are running and on the
same network. In Portainer, the `matam-tunnel` logs will say what it is failing
to connect to.

**Logging in appears to work but you are immediately logged out again.**
You reached the site over `http://`. Turn on **Always Use HTTPS**. Cookies are
marked `Secure` and a plain HTTP page cannot keep them.

**"عدد محاولات الدخول تجاوز الحد" appears for everyone at once.**
`TRUST_PROXY` is not set to `true`, so every visitor looks like the tunnel and
they share one rate limit. Set it and redeploy.

**Photo uploads fail.**
Check the container's Logs in Portainer. If it reports a permission problem, the
volume was created before the image set its ownership — remove the stack (keep
the volume), then redeploy.

**You cannot get into a dashboard.**
Use the password reset described in the README: set `ADMIN_PASSWORD` to a new
value and `ADMIN_PASSWORD_RESET` to `true` in the stack's environment
variables, update the stack, then set it back to `false` and update again.

---

## Alternative: your own reverse proxy

If you would rather not use a tunnel, delete the `cloudflared` service from the
stack, uncomment the `ports` line so the application listens on
`127.0.0.1:8080`, and put Nginx or Caddy in front of it.

Caddy is the shorter path, since it obtains a certificate on its own:

```
matam.example.org {
    reverse_proxy 127.0.0.1:8080
    request_body {
        max_size 8MB
    }
}
```

Keep `TRUST_PROXY=true`, and in Cloudflare's DNS set the record for the host to
**Proxied** (the orange cloud) so your server's address stays hidden.

With this arrangement you do need to forward ports 80 and 443 on the router, and
your server must have a reachable address.
