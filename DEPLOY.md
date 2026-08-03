# Deploying with Portainer on TrueNAS + Cloudflare — no terminal required

This is written for doing everything through a browser: TrueNAS's web UI,
Portainer's web UI, and the Cloudflare dashboard. No SSH, no command line.

Your domain: **malkiyaclub.online**
Your subdomain: **matam-alredha.malkiyaclub.online**

Two files matter, and they're different things — don't mix them up:

| File | Used for |
|---|---|
| `matam-alredha-build-context.tar.gz` | **Upload this into Portainer** to build the image |
| `matam-alredha.tar.gz` | A plain copy of the project, for reading or backup only |

---

## Step 1 — Build the image in Portainer

1. Open Portainer (usually `https://your-truenas-ip:9443` or wherever you access
   it) and go into the environment that manages your Docker apps.
2. In the left sidebar: **Images**.
3. **Build a new image.**
4. Give it a name: `matam-alredha:1.0`
5. Under **Build method**, choose **Upload**.
6. Click the upload box and select **`matam-alredha-build-context.tar.gz`** —
   the first file above. Leave the "Dockerfile name" field as the default
   (`Dockerfile`); this archive is arranged so the default just works.
7. **Build the image.**

This step compiles the whole Go application, so it takes a few minutes the
first time. Portainer shows a build log — watch for a line near the end saying
the image was built successfully. If it fails partway, scroll up in that log
and read the first red line; that's almost always the real error, not the last
line shown.

When it finishes, go to **Images** and confirm `matam-alredha:1.0` is listed.

> **Updating later:** when you have a newer version of the project, repeat this
> step with a new tag — `matam-alredha:1.1` — rather than overwriting `1.0`.
> That way you can always redeploy the old tag if something goes wrong.

---

## If your server already runs a Cloudflare connector

Skip creating a new tunnel. Instead:

1. Use **`docker-compose.portainer-existing-tunnel.yml`** as the stack file. It
   omits the `cloudflared` service and publishes the app on host port 8080.
2. Leave `CLOUDFLARE_TUNNEL_TOKEN` out of the environment variables entirely.
3. In Cloudflare, open your **existing** tunnel → **Public Hostnames** → **Add a
   public hostname**, with subdomain `matam-alredha`, domain
   `malkiyaclub.online`, type `HTTP`, and the URL pointing at this server:

   | How cloudflared runs | URL to use |
   |---|---|
   | As a container or TrueNAS app | `http://YOUR-SERVER-LAN-IP:8080` |
   | Directly on the host OS | `http://localhost:8080` |

   The LAN IP form works in both cases, so use that if unsure.

If port 8080 is already taken on your server, change only the left-hand number
in the stack's `ports:` line — for example `"8095:8080"` — and point the
Cloudflare hostname at that port instead.

Then continue from Step 3 below.

---

## Step 2 — Create the Cloudflare tunnel

1. Open [dash.cloudflare.com](https://dash.cloudflare.com) and select
   **malkiyaclub.online**.
2. Left sidebar → **Zero Trust** → **Networks** → **Tunnels**.
3. **Create a tunnel** → **Cloudflared** → name it `matam` → **Save**.
4. Cloudflare shows an install command with a long token in it. **Copy only the
   token** — the text after `--token`. You will paste it into Portainer, not
   run any command.
5. Still on the tunnel setup, go to **Public Hostnames** → **Add a public
   hostname**:

   | Field | Value |
   |---|---|
   | Subdomain | `matam-alredha` |
   | Domain | `malkiyaclub.online` |
   | Type | `HTTP` |
   | URL | `matam:8080` |

   `matam` is the container name inside the stack you're about to create, not a
   real address — Docker resolves it internally. Type is `HTTP`, not `HTTPS`;
   Cloudflare still serves your visitors over HTTPS, this inner hop just
   doesn't need its own certificate.

6. **Save.** Cloudflare adds the DNS record on its own — nothing to do in DNS
   settings by hand.

---

## Step 3 — Deploy the stack in Portainer

1. **Stacks** → **Add stack**.
2. Name: `matam`.
3. **Web editor** → paste the contents of `docker-compose.portainer.yml` from
   the project (open it in any text editor to copy it — Notepad, TextEdit,
   whatever you have).
4. Scroll down to **Environment variables**. Use the **Advanced mode** toggle
   so you can paste all of these at once, then fill in your own values on the
   right-hand side:

```
ADMIN_USERNAME=admin
ADMIN_PASSWORD=choose-a-strong-password
ELECTIONS_USERNAME=elections
ELECTIONS_PASSWORD=choose-a-different-strong-password
ADMIN_PATH=pick-your-own-secret-word
ELECTIONS_PATH=pick-a-different-secret-word
CLOUDFLARE_TUNNEL_TOKEN=paste-the-token-from-step-2
```

   `ADMIN_PATH` and `ELECTIONS_PATH` are **not** related to your domain or
   subdomain — they're the hidden dashboard addresses inside the site, e.g.
   `matam-alredha.malkiyaclub.online/ADMIN_PATH`. Pick two short, unguessable
   words. Letters, numbers, and hyphens only.

5. **Deploy the stack.**

Portainer creates the data volume automatically — nothing to set up on the
TrueNAS storage side for this. Give it a minute, then check **Containers**:
you should see `matam-alredha` and `matam-tunnel` both running, and
`matam-alredha` should show a green **healthy** status once it settles.

> **Set your passwords and paths before this first deploy.** The two dashboard
> accounts are created only on the very first start. Changing these
> environment variables afterwards and redeploying the stack has no effect —
> recovering a lost password afterward is covered in the README, but it's
> simpler to get it right now.

---

## Step 4 — Check it

Visit:

```
https://matam-alredha.malkiyaclub.online
```

You should land on the member login page.

Then your two dashboards, using whatever you set `ADMIN_PATH` /
`ELECTIONS_PATH` to:

```
https://matam-alredha.malkiyaclub.online/your-admin-path
https://matam-alredha.malkiyaclub.online/your-elections-path
```

You can also skip typing those: press and hold the small brass diamond at the
bottom of any page on a phone, or click it five times on a computer, and log in
through the box that opens.

**Confirm the member import worked.** In Portainer, click into the
`matam-alredha` container → **Logs**. Somewhere near the top you should see a
line reporting that 378 existing members were imported. That only happens once,
on the very first start.

---

## Step 5 — Cloudflare settings worth checking

Still in the dashboard for malkiyaclub.online:

**SSL/TLS → Overview** — encryption mode should be **Full**.

**SSL/TLS → Edge Certificates** — turn on **Always Use HTTPS**. This matters:
the login cookies are marked so they're only sent over HTTPS, so a visitor who
somehow lands on plain `http://` will look logged out even after signing in.

**Speed → Optimization** — leave **Rocket Loader** off. It rewrites JavaScript
and will break the pages here.

### Optional: lock the dashboards behind a Cloudflare login too

Under **Zero Trust → Access → Applications**, add a self-hosted application for
`matam-alredha.malkiyaclub.online/your-admin-path` (and the same for elections),
with a policy that only allows your own email address. Cloudflare then asks for
a one-time code sent to your email before the dashboard login page even loads —
a second lock in front of the first one.

---

## Backups

Everything that matters — the database, uploaded candidate photos, the print
template, and the generated spreadsheet — lives in the `matam-data` volume
Portainer created in Step 3.

In Portainer: **Volumes** → find `matam-data` (it will be named something like
`matam_matam-data`) — this tells you where it lives, but you don't need that
path for a GUI backup.

The most reliable GUI backup on TrueNAS: since Docker apps store their data on
whatever pool you assigned to the Docker/apps service, TrueNAS's own snapshot
schedule for that pool automatically covers this volume along with everything
else Docker-related. Set this up once under **Data Protection → Periodic
Snapshot Tasks** for the apps pool, with a daily schedule, and you have a
restorable copy without touching Docker at all.

Downloading the Excel file from the membership dashboard is a good habit but is
**not** a backup on its own — it holds the members only, not candidacies,
photographs, or votes. Take a real snapshot before and after election day.

---

## Updating later

1. Build a new image tag in Portainer (**Images → Build a new image**, same as
   Step 1, tagged e.g. `matam-alredha:1.1`) from the newer build-context file.
2. **Stacks → matam → Editor**, change `image: matam-alredha:1.0` to
   `matam-alredha:1.1`.
3. **Update the stack.**

Your data is untouched — it lives in the volume, not the image. Any new
database columns are added automatically when the new version starts.

---

## Troubleshooting

**Cloudflare shows error 502 or 1033.**
The tunnel can't reach the app. Recheck the public hostname: type `HTTP`, URL
exactly `matam:8080`. In Portainer, check both containers are in the **Running**
state and stopped/exited nowhere.

**You log in but get logged straight back out.**
Check **Always Use HTTPS** is on in Cloudflare.

**Everyone gets "too many login attempts" even on a first try.**
`TRUST_PROXY` should be `"true"` in the stack — it already is in
`docker-compose.portainer.yml`, so check you didn't accidentally overwrite it in
the environment variables section.

**Photo uploads fail with a server error.**
Open the `matam-alredha` container's **Logs**. If it mentions a permissions
problem, remove the stack (Portainer will ask separately about the volume —
say no, keep it) and redeploy; the image sets correct ownership on first start.

**You lost a dashboard password.**
In the stack's environment variables, set `ADMIN_PASSWORD` to a new value and
`ADMIN_PASSWORD_RESET` to `true`, then **Update the stack**. Once you've
confirmed the new password works, set `ADMIN_PASSWORD_RESET` back to `false`
and update again — leaving it `true` resets the password on every restart.
