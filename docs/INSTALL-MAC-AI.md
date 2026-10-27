# Встановлення YT Cartell Studio на Mac через ШІ-агента

Не хочеш розбиратися з Терміналом? Відкрий на Mac свого ШІ-агента, який уміє виконувати команди
(**Claude Code**, **Codex CLI**, Cursor-агент тощо), встав увесь текст із блоку нижче й запусти.
Агент сам завантажить останню версію для Mac з GitHub, перевірить контрольну суму, встановить програму
й усе потрібне для неї. Паролі (пароль Mac для Homebrew) вводиш тільки ти.

```text
Встанови на цей Mac програму YT Cartell Studio з офіційних релізів GitHub
(https://github.com/gitkalenyuk/yt-cartell-studio). Працюй крок за кроком і після кожного кроку
коротко пиши, що зроблено. Використовуй лише офіційні джерела: релізи цього репозиторію,
https://brew.sh та формули Homebrew. Паролі вводжу я сам — не вводь і не виводь їх.
Нічого не видаляй, крім старої копії самої програми.

1. Перевір систему: `sw_vers` (потрібна macOS 13 Ventura або новіша) і `uname -m`
   (arm64 = Apple Silicon, x86_64 = Intel).
2. Знайди найновішу версію для Mac:
   `curl -fsSL "https://api.github.com/repos/gitkalenyuk/yt-cartell-studio/releases?per_page=20"`
   Релізи йдуть від нових до старих; пропусти чернетки й попередні (draft, prerelease). Візьми
   перший реліз, де є файл під мою архітектуру: `YT-Cartell-Studio-<версія>-macos-arm64.zip`
   (Apple Silicon) або `YT-Cartell-Studio-<версія>-macos-x64.zip` (Intel); якщо його нема, підійде
   `…-macos-universal.zip`. Mac-збірка інколи виходить пізніше за Windows — якщо в найновішому
   релізі zip для macOS немає, скажи мені, яку версію ставиш замість неї. Якщо zip для macOS
   немає в жодному релізі — зупинись і скажи, що Mac-версія ще не опублікована.
3. Завантаж обраний zip і `SHA256SUMS.txt` з того ж релізу в `~/Downloads/yt-cartell-studio/`
   (curl -fL з адрес `browser_download_url`). Перевір контрольну суму: `shasum -a 256 <zip>`
   має збігтися з рядком цього файлу в `SHA256SUMS.txt`. Якщо не збігається — зупинись і
   нічого не встановлюй.
4. Якщо програма вже запущена — закрий її: `osascript -e 'quit app "YT Cartell Studio"'`.
   Розпакуй: `ditto -x -k <zip> ~/Downloads/yt-cartell-studio/app`. Якщо в /Applications вже є
   «YT Cartell Studio.app» — перейменуй її на «YT Cartell Studio.old.app», скопіюй нову
   (`ditto "<…>/YT Cartell Studio.app" "/Applications/YT Cartell Studio.app"`), а після успішного
   запуску видали стару копію.
5. Програма не підписана сертифікатом Apple, тому зніми карантин:
   `xattr -dr com.apple.quarantine "/Applications/YT Cartell Studio.app"`.
6. Потрібні інструменти:
   - Homebrew: `command -v brew`; якщо нема — встанови офіційним скриптом з https://brew.sh і
     виконай рядок `eval "$(… brew shellenv)"`, який він підкаже.
   - `brew install ffmpeg` — без FFmpeg програма не змонтує відео.
   - Запитай мене, чи потрібне локальне розпізнавання мови (для «Своєї озвучки» без
     інтернету); якщо так — `brew install whisper-cpp`.
7. Запусти: `open -a "YT Cartell Studio"` — має відкритися вікно студії.
   Перевір, що в `~/Library/Application Support/YT Cartell Studio/studio.log` немає
   слова panic.
8. Прибери завантажені файли з `~/Downloads/yt-cartell-studio/` і напиши мені звіт: встановлена
   версія, архітектура, що довстановлено (ffmpeg, whisper-cpp), і що робити далі:
   - встановити G-Labs Studio (https://duckmartians.info/g-labs/en/) для кадрів і відео,
     увімкнути в ній Webhook API (Generate → Start Server) і вписати адресу
     http://127.0.0.1:8765 та ключ у «Налаштування → Кадри й відео» студії;
   - вписати текстовий сервіс (CarteLink, будь-який OpenAI-сумісний, Anthropic, Ollama, Azure
     чи Gemini) і ключ озвучки (наприклад ElevenLabs) у «Налаштування → Підключення» — або
     вставити всі адреси й ключі в «Помічник підключення» там само, він розбереться сам;
   - коли вперше натисну мікрофон у «Новій історії», macOS спитає дозвіл на мікрофон — дозволити.
```

## Що робить агент (коротко)

1. Бере найновіший реліз з [Releases](https://github.com/gitkalenyuk/yt-cartell-studio/releases), у якому є zip під твій Mac.
2. Звіряє SHA-256 з `SHA256SUMS.txt` — підмінений файл не встановиться.
3. Кладе «YT Cartell Studio.app» у Програми й знімає карантин (застосунок без підпису Apple).
4. Ставить FFmpeg (і, якщо скажеш, whisper.cpp) через Homebrew.
5. Запускає студію й пояснює, що налаштувати далі.

Оновлення потім приходять прямо в програмі: кнопка **«Оновлення»** в меню зліва.

Що вміє нова версія і як цим користуватися — у [довіднику](https://gitkalenyuk.github.io/yt-cartell-studio/guide/).
