---
title: "Umami's interface for platform administrators"
description: "How a platform administrator with SSH access to a core node opens Umami's own interface for debugging, over a tunnel to the node's loopback."
---

Projects and SCF operators read web analytics in the console. Umami's own interface has no
public host ([ADR 0005](../adr/0005-umami-as-engine-only.md)): no DNS record, no Caddy site and
no certificate serve it. This page is for **platform administrators** who hold SSH access to
the nodes and need Umami's interface to debug the engine, for example to compare a figure with
what Umami's own pages show after an upgrade.

Umami process 1 on every core node publishes its port on the node's loopback only,
`127.0.0.1:3300` (`core_umami_admin_port` in `infra/ansible/roles/core/defaults/main.yml`).
Nothing outside the node reaches it.

## Open the interface

1. Open a tunnel to a core node:

   ```sh
   ssh -L 3300:127.0.0.1:3300 ops@<core node public address>
   ```

2. Get a session token for the admin service account. Umami runs with `DISABLE_LOGIN=1`, so its
   login page answers 403; the login route still works. The password is `umami_admin_password`
   from the environment's SOPS file and travels on standard input, never on a command line:

   ```sh
   sops --config infra/secrets/.sops.yaml decrypt --extract '["umami_admin_password"]' \
     infra/secrets/<env>/secrets.enc.yaml |
     node -e 'let p="";process.stdin.on("data",(d)=>p+=d).on("end",async()=>{
       const r=await fetch("http://127.0.0.1:3300/api/auth/login",{method:"POST",
         headers:{"content-type":"application/json"},
         body:JSON.stringify({username:"admin",password:p.trim()})});
       if(!r.ok){console.error("login",r.status);process.exit(1)}
       console.log((await r.json()).token)})'
   ```

3. Open `http://127.0.0.1:3300/sso?token=<token>&url=/websites/<website id>` in a browser on
   the same machine. The website id of a site's counted traffic is the site id; its
   quarantine website id is `quarantine_website_id` of the site.

Close the tunnel when you are done. The token ends with the next change of the admin password.

## What this access is

- The admin service account sees every project's raw events and sessions. The same people
  already hold that data through the database on the node, so the tunnel adds no new holder.
- Never publish this port, add a Caddy site for Umami or give a project a link to it. The
  isolation gate's Caddy check (`make test-caddy-closed`) fails when an internet-facing Caddy
  site refers to Umami.
- Use the interface to read. Changes to websites, teams and users belong to the reconciler,
  which repairs any difference on its next run.
