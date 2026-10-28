# ADR 0008: Artifact Storage Format

> **Project:** Fedora Phoenix
> **Status:** ✅ Accepted
> **Date:** 2026-02-03
> **Updated:** 2026-10-29 — symlink 收錄與還原（3b）、`!` 排除規則
> **Refers to:**
> - [ADR 0003](./adr-0003-dotfiles-management.md)
> - [ADR 0007](./adr-0007-artifact-sync-harvesting.md)

---

## 📋 Context (背景)

Phoenix 透過 `phoenix.yml` 處理可從網路取得的套件（DNF packages），但有一類資料無法從網路還原：

| 類型 | 範例 | 特性 |
|------|------|------|
| **個人檔案** | 文件、專案、資料 | 大小不定、可能很肥 |
| **Dotfiles** | `~/.zshrc`, `~/.config/*` | 小型文字檔為主 |
| **程式狀態** | `/var/lib/*`, `/opt/*` 內的設定 | 應用程式產生的狀態資料 |
| **機密資料** | SSH keys, GPG keys, tokens | 不能公開、需加密保護 |

### 問題

1. **Git 不適合大檔案** — 二進位檔、媒體檔會讓 repo 爆炸
2. **權限資訊遺失** — Git 無法記錄完整的 Unix 權限（Owner, Group, Mode）
3. **需要本地備份** — 系統重灌前需要完整的狀態快照

---

## 🎯 Decision (決策)

### 1. 單一 Artifact 檔案

所有備份內容壓縮為單一 `.tgz` 檔案。

### 2. 內部結構：Filesystem Mirror + Metadata

tgz 解壓後有一層保護資料夾，命名格式為 `phoenix-backup-<YYYYMMDD>`。

```
phoenix-backup-20260203.tgz
└── phoenix-backup-20260203/      ← 保護層（含日期）
    ├── filemeta.yml              ← 權限與擁有者資訊（Metadata）
    ├── blueprint.yml             ← 打包當下使用的 blueprint 原檔（可選，見下方說明）
    └── fs/                       ← 檔案系統鏡像（內容）
        ├── home/
        │   └── <username>/
        │       ├── .zshrc
        │       ├── .config/
        │       │   └── nvim/
        │       │       └── init.lua
        │       └── .ssh/
        │           ├── config
        │           └── id_ed25519
        ├── var/
        │   └── lib/
        │       └── some-app/
        │           └── settings.db
        └── opt/
            └── some-tool/
                └── config.yml
```

**原則**：`fs/` 內的路徑 = 系統上的絕對路徑

- `fs/home/user/.zshrc` → `/home/user/.zshrc`
- `fs/var/lib/app/data` → `/var/lib/app/data`

---

### 3. filemeta.yml：權限追蹤機制

#### 設計理念

由於 Git 無法記錄完整的 Unix 權限資訊，我們使用 `filemeta.yml` 存放 Metadata。

#### filemeta.yml 格式

```yaml
version: "1.0"
generated_at: "2026-02-03T10:30:00Z"

entries:
  - path: "/home/user/.zshrc"
    mode: "0644"
    owner: "user"
    group: "user"
    sha256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

  - path: "/home/user/.ssh/id_ed25519"
    mode: "0600"
    owner: "user"
    group: "user"
    sha256: "..."

  - path: "/home/user/.config/nvim"
    mode: "0755"
    owner: "user"
    group: "user"
    type: "directory"

  - path: "/home/user/project/CLAUDE.md"
    mode: "0777"
    owner: "user"
    group: "user"
    type: "symlink"
    target: "AGENTS.md"
```

#### 欄位說明

| 欄位 | 型別 | 說明 |
|------|------|------|
| `path` | string | 絕對路徑 |
| `mode` | string | 八進位權限 (e.g., "0644", "0755") |
| `owner` | string | 擁有者名稱（**不記錄 UID**） |
| `group` | string | 群組名稱（**不記錄 GID**） |
| `sha256` | string | 檔案內容雜湊（用於驗證完整性），只有一般檔案有 |
| `type` | string | 可選，`"directory"` 或 `"symlink"`；省略代表一般檔案 |
| `target` | string | 只有 symlink 有：`readlink` 讀到的原始目標（見 3b） |
| `mod_time` | string | 可選，RFC3339 修改時間；還原時用來和磁碟上同路徑的檔案比新舊 |

**重要**：
- 只記錄 owner/group **名稱**，不記錄 UID/GID
- UID/GID 應在 provision 時透過系統指令建立後由程式從 OS 讀回
- 不應 hardcoding 在任何檔案裡面

---

### 3a. blueprint.yml：自帶還原配方

**設計理念**：`blueprint.yml` 是選配的，放在保護層根目錄（與 `filemeta.yml` 同層，不進 `fs/`），內容是打包當下所使用的 blueprint 原檔的逐字複製。

**目的**：讓單一 artifact tgz 具備「自我描述」能力——還原時若沒有明確指定要用哪份 blueprint，可以直接從 archive 裡讀出這份內建版本，不需要事先另外準備一份 blueprint 檔案。固定的檔案位置本身就是協議的一部分，讀取時不需要預先知道 blueprint 的內容才能找到它。

**優先順序**：明確指定的 blueprint 來源永遠優先於 artifact 內建版本；內建版本只在沒有明確指定時才會被採用。

---

### 3b. Symlink：只記連結，不跟進目標

**背景**：最早的版本只收一般檔案，symlink 會被略過。結果像 `CLAUDE.md -> AGENTS.md` 這種專案慣用的連結，還原後就消失了；而且不會有人注意到少了它。

**決策**：

- 打包時**不跟進** symlink，只記錄連結本身：`type: "symlink"`，`target` 存 `readlink` 讀到的原始字串。
  - 相對路徑維持相對，絕對路徑維持絕對，不做任何改寫。
  - 指向不存在目標的 dangling link 照樣收錄。
- symlink 在 `fs/` 底下**沒有對應內容**，filemeta.yml 的這一筆就足以重建它。
- `mode` 照實記錄，但還原時不套用：Linux 會忽略 symlink 的權限位元。
- 還原時：
  - 該路徑沒有東西：建立 symlink，再用 `lchown` 設定擁有者（不能用 `chown`，否則會改到目標）。
  - 已經有同一個 target 的 symlink：只更新擁有者。
  - 已經有其他東西（不同 target 的 symlink、一般檔案或目錄）：**保留磁碟上的現況**，在 notices 裡回報。不覆蓋，因為覆蓋可能默默吃掉真正的內容。這和一般檔案「保留較新者」的精神一致。

Socket、FIFO、裝置檔等其他特殊檔案仍然略過。

---

### 4. 大檔案處理

**決策**：大檔案（影片、VM images、Docker images）同樣存放於 `fs/` 目錄，不排除。

**Git 版控策略**：
- Artifact 內可包含 `.git/` 進行版本控制（可選）
- 使用 `.gitignore` 排除大檔案，避免進入 Git history
- 大檔案仍然存在於 Artifact 的 `fs/` 目錄中，只是不被 Git 追蹤

**範例 .gitignore**：
```
# 排除大檔案/目錄，但保留在 Artifact 中
fs/home/user/Videos/
fs/home/user/Downloads/
fs/home/user/.cache/
```

---

### 5. Git 的角色

**決策**：可選擇性包含 `.git/` 資料夾

**使用場景**：
- 使用者可以在 Artifact 目錄內進行 `git init`
- 透過 `.gitignore` 排除大檔案
- 將小型設定檔進行版本控制
- Harvest 時決定是否包含 `.git/` 到最終的 `.tgz`

---

## 🖇️ Schema Changes (phoenix.yml)

### harvest 區塊

```yaml
userspace:
  harvest:
    # 要收集的路徑
    paths:
      - "~/.zshrc"
      - "~/.config/nvim/"
      - "~/.ssh/"
      - "~/.gitconfig"
      - "~/Documents/"
      - "~/Videos/"  # 大檔案也要收集
```

### 排除規則：`!` 前綴（2026-10-29 新增）

> 區塊名稱在 ADR 0009 之後改為 `userspace.dehydration`。

`paths` 裡以 `!` 開頭的項目代表**排除**，概念類似 `.gitignore` 的否定規則，只是方向相反：一般項目是「要收」，`!` 項目是「不收」。

```yaml
userspace:
  dehydration:
    paths:
      - "~/workshop"
      - "!~/workshop/dev/coding-agent/workspace"   # 這個目錄和底下所有東西都不收
      - "!~/workshop/dev/*/node_modules"           # 含 glob 字元時，對整條路徑做比對
```

- 不管 `!` 項目寫在哪裡，都對**所有**一般項目生效，跟順序無關。
- 比對對象是展開 `~` 之後的絕對路徑：
  - 完全相同，或位在該路徑底下 → 排除。
  - 含 `*`、`?`、`[` 時，用 Go 的 `filepath.Match` 比對整條路徑。`*` 不會跨越 `/`，也不支援 `**`。
- 排除的判斷發生在讀取目錄**之前**，所以使用者自己讀不到的目錄（例如被別的 UID 持有）只要被排除，就不會讓打包失敗。

### system 區塊新增 users 和 groups

```yaml
system:
  packages:
    - git
    - zsh

  # 使用者與群組管理
  users:
    - name: "docker"
      system: true
      groups:
        - docker

    - name: "devuser"
      system: false
      groups:
        - wheel  # sudoer via wheel group
        - docker

  groups:
    - name: "docker"
      system: true
```

**重要**：
- 只記錄 `name`，不記錄 `id`（UID/GID）
- `id` 應在呼叫系統指令（如 `useradd`, `groupadd`）建立後由程式從 OS 讀回
- 不應 hardcoding 在任何檔案裡面
- 使用者的權限透過 `groups` 欄位管理（例如：wheel, docker, sudo）

---

## ⚖️ Consequences (後果)

### ✅ 正面影響 (Pros)

| 優勢 | 說明 |
|------|------|
| **完整性** | 單一 Artifact 包含所有必要資料與 Metadata |
| **權限精確** | 透過 filemeta.yml 記錄完整的 Unix 權限資訊 |
| **可驗證** | SHA256 確保檔案完整性 |
| **簡單直觀** | Filesystem Mirror 結構清晰易懂 |
| **支援大檔案** | 不受 Git 限制，可包含任意大小檔案 |
| **彈性版控** | 可選擇性使用 Git，透過 .gitignore 控制追蹤範圍 |

### ❌ 負面影響 (Cons)

| 劣勢 | 說明 |
|------|------|
| **體積較大** | 包含大檔案時 Artifact 體積會很大 |
| **需手動管理** | 使用者需自行決定 Git 版控策略 |

---

## 💡 Implementation Notes

### Harvest 流程

1. 讀取 `phoenix.yml` 的 `userspace.harvest` 設定
2. 掃描所有 `paths` 路徑（包含大檔案）
3. 複製檔案到 `fs/` 目錄（保持原始路徑結構）
4. 記錄每個檔案的 Metadata 到 `filemeta.yml`
5. 打包成 `phoenix-backup-<YYYYMMDD>.tgz`

### Provision 流程

1. 解壓 Artifact
2. 讀取 `filemeta.yml`
3. 從 `fs/` 複製檔案到系統對應位置；symlink 依 `target` 重建（見 3b）
4. 根據 filemeta.yml 設定正確的 owner, group, mode（symlink 只設 owner/group，用 `lchown`）
5. 驗證 SHA256 確保完整性
