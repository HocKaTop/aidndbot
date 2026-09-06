"""Configure the bot's Mini App menu and local origin without displaying secrets."""
import argparse
import json
from pathlib import Path
import sys
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('url', help='Public HTTPS origin of the Mini App')
    parser.add_argument('--check', action='store_true', help='Only inspect the configured menu')
    args = parser.parse_args()
    url = args.url.rstrip('/')
    parsed = urllib.parse.urlsplit(url)
    if (parsed.scheme != 'https' or not parsed.hostname or parsed.username
            or parsed.password or parsed.path or parsed.query or parsed.fragment):
        parser.error('Use a public HTTPS origin, without a path or credentials.')
    env_file = Path(__file__).resolve().parent.parent / '.env'
    lines = env_file.read_text().splitlines()
    values = {}
    for line in lines:
        if '=' in line and not line.lstrip().startswith('#'):
            key, value = line.split('=', 1)
            values[key.strip()] = value.strip().strip('\"\'')
    token = values.get('TELEGRAM_BOT_TOKEN')
    if not token:
        parser.error('TELEGRAM_BOT_TOKEN is missing from .env.')

    def call(method, data):
        request = urllib.request.Request(
            'https://api.telegram.org/bot' + token + '/' + method,
            data=json.dumps(data).encode(),
            headers={'Content-Type': 'application/json'},
        )
        with urllib.request.urlopen(request, timeout=25) as response:
            body = json.load(response)
        if not body.get('ok'):
            raise RuntimeError('Telegram rejected menu configuration.')
        return body.get('result')

    if not args.check:
        call('setChatMenuButton', {'menu_button': {
            'type': 'web_app', 'text': 'Открыть игру', 'web_app': {'url': url},
        }})
    menu = call('getChatMenuButton', {})
    if args.check:
        print(json.dumps(menu, ensure_ascii=False))
        return
    if menu.get('type') != 'web_app' or menu.get('web_app', {}).get('url', '').rstrip('/') != url:
        raise RuntimeError('Telegram menu verification failed.')
    updates = {'APP_BASE_URL': url, 'BOT_ENABLED': 'true'}
    for key, value in updates.items():
        lines = [line for line in lines if line.split('=', 1)[0].strip() != key]
        lines.append(key + '=' + value)
    env_file.write_text('\n'.join(lines) + '\n')
    print('Telegram Mini App menu configured and verified.')
    print('APP_BASE_URL updated:', url)
    print('Apply local settings: docker compose up -d backend')


if __name__ == '__main__':
    try:
        main()
    except urllib.error.URLError:
        # urllib exceptions may include a URL containing the token. Never print them.
        print('Telegram API network/HTTP request failed; no token values displayed.', file=sys.stderr)
        sys.exit(1)
    except RuntimeError as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError):
        print('Configuration failed; check .env and Telegram connectivity.', file=sys.stderr)
        sys.exit(1)
