# Інструкція для ШІ-агента на Mac: зібрати й встановити YT Cartell Studio

Відкрий папку з кодом студії (клон репозиторію або свого форку — та, де лежать `go.mod` і
`build-mac.sh`) у Claude Code, Codex чи іншому агенті на MacBook, встав увесь текст нижче (від лінії
до кінця файлу) і запусти. Інструкція не прив'язана ні до версії, ні до репозиторію: агент збирає
той код, що лежить у цій папці, і бере версію з `internal/version/version.go`.

---

Ти — збирач YT Cartell Studio на macOS. Твоє завдання: зібрати Mac-версію програми з коду в поточній
папці, встановити її на цей Mac і — лише якщо я попрошу — докласти збірку до релізу на GitHub.
Працюй крок за кроком, після кожного кроку коротко пиши, що зроблено. Якщо команда падає — прочитай
помилку, виправ причину й повтори; не пропускай кроки мовчки.

Правила:
- Паролі, токени й коди входу вводжу я сам (Homebrew попросить пароль Mac, `gh auth login`
  відкриє браузер). Ти їх не вводиш і не виводиш у чат.
- Нічого не видаляй поза папками цієї інструкції. Не чіпай інші репозиторії.
- Нічого не комить, не пуш, не створюй релізів і тегів без мого «так».

## 1. Перевір систему
- `sw_vers` — потрібна **macOS 13 Ventura або новіша** (Go 1.27 старіші не підтримує; програма теж
  вимагає 13+). Старіша — зупинись і скажи мені.
- `uname -m` — `arm64` = Apple Silicon, `x86_64` = Intel.
- Переконайся, що ти в папці з кодом: `ls go.mod build-mac.sh`. Якщо їх нема, а є підпапка `GO` —
  перейди в неї. Якщо нема ні того, ні того — спитай мене, де лежить код.

## 2. Інструменти
- Xcode Command Line Tools: `xcode-select -p`; якщо нема — `xcode-select --install`
  і чекай, поки я натисну «Встановити» у вікні, що відкриється (перевіряй кожні 30 с).
- Homebrew: `command -v brew`; якщо нема — встанови офіційним скриптом з https://brew.sh
  (`/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`)
  і виконай рядок `eval "$(/opt/homebrew/bin/brew shellenv)"` (Apple Silicon) або
  `eval "$(/usr/local/bin/brew shellenv)"` (Intel), який він підкаже.
- `brew install go ffmpeg` (якщо вже встановлені — `brew upgrade go ffmpeg`).
  Перевір `go version`: потрібна Go 1.27 або новіша. FFmpeg потрібен самій студії (монтаж,
  мікрофон, експорт у редактори), для збірки — ні.
- Запитай мене, чи потрібне локальне розпізнавання мови («Своя озвучка» й мікрофон без
  інтернету). Якщо так — `brew install whisper-cpp` (на Mac офіційної збірки whisper.cpp немає,
  студія бере її з Homebrew).

## 3. Код
- Покажи `git status --short` і `git log -3 --oneline` (якщо це git-клон). Спитай, чи підтягнути
  свіжий код (`git pull --ff-only`); локальні зміни не скидай.
- **Версія:** `sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/version/version.go`.
  Далі в інструкції `<версія>` — це вона (напр. `2.1.0`).
- Прочитай `docs/BUILD-MAC.md` — це докладний посібник до скрипта збірки — і `ARCHITECTURE.md`
  (правила проєкту), якщо доведеться щось виправляти в коді.
- Переконайся, що в `build-mac.sh` є `MIN_MACOS="13.0"` і ключ `NSMicrophoneUsageDescription`
  (дозвіл на мікрофон для диктування в «Новій історії»): `grep -n 'MIN_MACOS=\|NSMicrophoneUsageDescription' build-mac.sh`.

## 4. Перевірка коду на Mac
- `go vet ./...` і `go build ./...` (на Mac cgo увімкнений за замовчуванням — так і треба).
- Якщо збірка падає через код під macOS — виправ мінімально (лише macOS-специфічне), переконайся,
  що Windows-збірка не зламалась: `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`,
  прожени `go test ./internal/...` і покажи мені зміни. Комітити — лише з мого «так».

## 5. Збірка
- `bash build-mac.sh` — збере `YT Cartell Studio.app` для Apple Silicon, Intel і універсальну
  та запакує в `dist/mac/YT-Cartell-Studio-<версія>-macos-arm64.zip`, `…-macos-x64.zip`,
  `…-macos-universal.zip` + `SHA256SUMS.txt`. Якщо Intel-збірка на Apple Silicon не вдалася —
  скрипт збере решту; скажи мені про це.
- Перевір Info.plist зібраної програми:
  `unzip -p dist/mac/YT-Cartell-Studio-<версія>-macos-arm64.zip "YT Cartell Studio.app/Contents/Info.plist" > /tmp/ytc-Info.plist && plutil -p /tmp/ytc-Info.plist | grep -E 'CFBundleShortVersionString|LSMinimumSystemVersion|NSMicrophoneUsageDescription'`
  — версія `<версія>`, мінімум `13.0`, є опис для мікрофона.
- **Публікація (лише якщо я попросив).** Спитай, у який репозиторій (`власник/назва`); реліз
  `v<версія>` там має вже існувати (його створює `release.ps1` на Windows). Потрібен GitHub CLI:
  `brew install gh`, `gh auth status` (якщо не увійдено — `gh auth login --hostname github.com --git-protocol https --web`
  і чекай, поки я підтверджу вхід у браузері). Тоді `bash build-mac.sh --upload --repo власник/назва`
  і перевір `gh release view v<версія> -R власник/назва --json assets --jq '.assets[].name'`.

## 6. Встанови на цей Mac
- Візьми zip для своєї архітектури з `dist/mac/` (arm64 або x64).
- Якщо програма запущена — закрий: `osascript -e 'quit app "YT Cartell Studio"'` (студія
  відкривається лише в одній копії, тож стара мусить закритися).
- Розпакуй у тимчасову папку (`rm -rf /tmp/ytc-install && ditto -x -k <zip> /tmp/ytc-install`) і заміни
  `/Applications/YT Cartell Studio.app` новою: стару (якщо є) спершу перейменуй у
  `/Applications/YT Cartell Studio.old.app`, скопіюй нову через `ditto`, а після успішного запуску
  видали `.old.app` і `/tmp/ytc-install`.
- `xattr -dr com.apple.quarantine "/Applications/YT Cartell Studio.app"`.
- Запусти: `open -a "YT Cartell Studio"`. Переконайся, що відкрилося вікно з головною сторінкою
  («Доброго дня! Що знімемо сьогодні?») і що в `~/Library/Application Support/YT Cartell Studio/studio.log`
  немає слова `panic`.

## 7. Звіт
Напиши коротко:
- версія, коміт, з якого зібрано (якщо це git-клон), архітектура цього Mac;
- які файли зібрано (назви й розміри), чи щось опубліковано і куди;
- шлях до встановленої програми; чи були виправлення коду (які);
- що лишилось зробити мені:
  - підключити свої сервіси в «Налаштування → Підключення» (FFmpeg студія знайде з Homebrew);
  - коли вперше натисну мікрофон у «Новій історії», macOS спитає дозвіл на мікрофон — дозволити
    (якщо відмовив: Системні параметри → Конфіденційність і безпека → Мікрофон);
  - наступні версії ставити з самої програми («Оновлення» в меню зліва) або зібрати знову цією ж
    інструкцією.
