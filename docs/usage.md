# Script Options

`./manage.sh` wraps the root [`compose.yaml`](../compose.yaml). Each section below also shows the plain
`docker compose` equivalent (run from the repo root; `COMPOSE_PROFILES` in `.env` selects the services).

## Usage

```
Usage:
    ./manage.sh -n <network> -a <action> <optional args>
        -n|--network: [ mainnet | testnet ]
        -a|--action: [ start | stop | logs | reset | upgrade | import | export | bns ]
    optional args:
        -f|--flags: [ signer,proxy,postgres,monitoring ]
        export: combined with 'logs' action, exports logs to a text file
    ex: ./manage.sh -n mainnet -a start -f proxy
	ex: ./manage.sh -n mainnet -a start -f signer,proxy
    ex: ./manage.sh --network mainnet --action start --flags proxy
    ex: ./manage.sh -n mainnet -a logs export
```

### Starting Services

```bash
./manage.sh -n <network> -a start
```

```bash
./manage.sh -n <network> -a restart
```

#### With optional signer

```bash
./manage.sh -n <network> -a start -f signer
```

```bash
./manage.sh -n <network> -a restart -f signer
```

#### With optional proxy

```bash
./manage.sh -n <network> -a start -f proxy
```

```bash
./manage.sh -n <network> -a restart -f proxy
```

#### With monitoring

Prometheus and Grafana on 127.0.0.1; set `GRAFANA_ADMIN_PASSWORD` in `.env` first. See [Monitoring](./monitoring.md).

```bash
./manage.sh -n <network> -a start -f monitoring
```

---

### Stopping Services

```bash
./manage.sh -n <network> -a stop
# or: docker compose --profile '*' down
```

The node is stopped first and gets up to `STACKS_SHUTDOWN_TIMEOUT` seconds to finish its run loop;
don't pass `-t` to `down`, which would override that. A stack started with an older version of this
repo (the `compose-files/` layout) is stopped the same way by `./manage.sh -a stop`.

---

### Event Replay

#### Export stacks-blockchain-api events

```bash
./manage.sh -n <network> -a export
./manage.sh -n <network> -a logs # check logs for completion
./manage.sh -n <network> -a restart
# or, with the services stopped:
#   EVENT_REPLAY_ACTION=export docker compose --profile event-replay up -d
#   docker compose logs -f event-replay
```

#### Replay stacks-blockchain-api events

The events file must exist in this network's event-replay dir (default `./persistent-data/<network>/event-replay/`).
The import wipes the API database first.

```bash
./manage.sh -n <network> -a import
./manage.sh -n <network> -a logs # check logs for completion
./manage.sh -n <network> -a restart
# or, with the services stopped: EVENT_REPLAY_ACTION=import docker compose --profile event-replay up -d
```

---

### Logging

#### Stream logs to terminal

```bash
./manage.sh -n <network> -a logs
```

#### Export docker log files

This will create a log file in `./exported-logs` for every running service. \
_Note that each time you run this command the log files will be overwritten._

```bash
./manage.sh -n <network> -a logs export
```

---

### Ensure all images are up to date

```bash
./manage.sh -n <network> -a pull
```

### Delete all chainstate data

Empties this network's data dirs (the dirs themselves are kept, so dedicated mounts keep working) after
listing them and asking for confirmation. Some of the data is owned by root, so this will need to run
with **sudo** privileges.

```bash
sudo ./manage.sh -n <network> -a reset
```

### Download BNS data

Store legacy BNS data to `./persistent-data/bns-data` \
_This step is required if the env var `BNS_IMPORT_DIR` is uncommented_

```bash
./manage.sh bns
```
