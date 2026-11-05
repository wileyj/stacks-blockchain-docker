# Docker

- [Install Docker Compose](https://docs.docker.com/compose/install/)
- [Docker Desktop for Mac](https://docs.docker.com/docker-for-mac/install/)
- [Docker Desktop for Windows](https://docs.docker.com/docker-for-windows/install/)
- [Docker Engine for Linux](https://docs.docker.com/desktop/install/linux-install/#generic-installation-steps)

## Verify `docker compose` is installed locally

`docker compose` version `2.24.0` or greater is required (`2.27.2` or later recommended):

```bash
$ docker compose version
Docker Compose version v2.27.2
```

## Install/Upgrade docker compose

If the command is not found, or the version is less than `2.24.0`, follow the [official Docker Compose installation instructions](https://docs.docker.com/compose/install/) for your platform.

For Docker Desktop users (Mac/Windows), Compose is included by default.

For Linux users, install the docker-compose-plugin:

```bash
sudo apt-get update
sudo apt-get install docker-compose-plugin
```

## Security note on docker

The Docker daemon always runs as the root user so you will need root privileges to interact with it.

To run `docker compose` (and `sbd`) without `sudo`, _manage Docker as a non-root user_ by following
[these steps](https://docs.docker.com/engine/install/linux-postinstall/#manage-docker-as-a-non-root-user).
Membership in the `docker` group is equivalent to root on that host. Nothing in this repo needs `sudo`
otherwise: deleting, seeding and upgrading data run in containers.

## Debian based install

Copy/paste the following _should_ work to get Docker Engine with the Compose plugin and git on a
Debian based host (see [Docker's instructions](https://docs.docker.com/engine/install/debian/) for
others):

```bash
sudo apt-get update && sudo apt-get install -y git curl
sudo apt-get install -y ca-certificates     curl     gnupg     lsb-release
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo   "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian \
  $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update && sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
sudo groupadd docker
sudo usermod -aG docker $USER
```
