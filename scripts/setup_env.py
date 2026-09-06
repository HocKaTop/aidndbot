"""Complete local configuration without printing or replacing existing secrets."""
from pathlib import Path
import secrets

path = Path(__file__).resolve().parent.parent / '.env'
text = path.read_text() if path.exists() else ''
values = {}
for line in text.splitlines():
    if '=' in line and not line.lstrip().startswith('#'):
        key, value = line.split('=', 1)
        values[key.strip()] = value.strip()
# Accept the initial name used during project setup.
if not values.get('TELEGRAM_BOT_TOKEN') and values.get('telegram_bot_api'):
    text = '\n'.join('TELEGRAM_BOT_TOKEN=' + line.split('=', 1)[1].strip()
                     if line.split('=', 1)[0].strip() == 'telegram_bot_api' else line
                     for line in text.splitlines()) + '\n'
    values['TELEGRAM_BOT_TOKEN'] = values['telegram_bot_api']
defaults = {
    'POSTGRES_PASSWORD': secrets.token_hex(24),
    'JWT_SECRET': secrets.token_hex(32),
    'APP_BASE_URL': 'http://localhost:8080',
    'OLLAMA_URL': 'http://host.docker.internal:11434',
    'DEFAULT_OLLAMA_MODEL': 'qwen3:8b',
    'BOT_ENABLED': 'false',
}
for key, value in defaults.items():
    if not values.get(key):
        lines = text.splitlines()
        lines = [line for line in lines if line.split('=', 1)[0].strip() != key]
        text = '\n'.join(lines) + '\n' + key + '=' + value + '\n'
path.write_text(text)
path.chmod(0o600)
print('Local .env prepared. Existing secrets preserved; no secret values displayed.')
