"""Start the app with a Quick Tunnel and configure Telegram using its current URL."""
from pathlib import Path
import re
import subprocess
import sys
import time


ROOT = Path(__file__).resolve().parent.parent
COMPOSE = ['docker', 'compose', '-f', 'docker-compose.yml', '-f', 'compose.tunnel.yml']
URL_PATTERN = re.compile(r'https://[a-z0-9]+(?:-[a-z0-9]+)*\.trycloudflare\.com\b')


def run(args, capture=False):
    return subprocess.run(
        args, cwd=ROOT, check=True, text=True,
        capture_output=capture, timeout=30 if capture else None,
    )


def wait_for_url(timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        container = run(COMPOSE + ['ps', '-q', 'tunnel'], capture=True).stdout.strip()
        if container:
            # A restarted container keeps old logs; only use this run's address.
            started = run([
                'docker', 'inspect', '--format', '{{.State.StartedAt}}', container,
            ], capture=True).stdout.strip()
            logs = run(COMPOSE + [
                'logs', '--no-color', '--since', started, 'tunnel',
            ], capture=True)
            urls = URL_PATTERN.findall(logs.stdout + logs.stderr)
            if urls:
                return urls[-1]
        time.sleep(2)
    raise RuntimeError('Tunnel URL did not appear within 90 seconds. Run make tunnel-logs and retry make tunnel.')


def main():
    print('Starting the app and Cloudflare Tunnel...', flush=True)
    run(COMPOSE + ['up', '-d', '--build'])
    print('Waiting for the tunnel URL...', flush=True)
    url = wait_for_url()
    run([sys.executable, str(ROOT / 'scripts/configure_telegram.py'), url])
    run(COMPOSE + ['up', '-d', 'backend'])
    print(f'\nMini App: {url}\nTelegram menu configured. Open your bot.')
    print('If you configured Main Mini App in BotFather, update its URL there too.')


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError:
        print('A setup command failed. Fix the error and retry make tunnel.', file=sys.stderr)
        sys.exit(1)
    except subprocess.TimeoutExpired:
        print('Docker did not respond in time. Check Docker and retry make tunnel.', file=sys.stderr)
        sys.exit(1)
    except RuntimeError as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except OSError:
        print('Could not run setup. Check that Docker and Python are available.', file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print('\nSetup interrupted. To stop the tunnel: make tunnel-down', file=sys.stderr)
        sys.exit(130)
