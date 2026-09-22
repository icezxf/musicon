# MusicOn Go

AList 音乐库 + Subsonic 协议服务，单二进制部署。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| LISTEN | 0.0.0.0:8000 | 监听 |
| DB_PATH | ./data/musicon.db | SQLite 路径 |
| DATA_DIR | ./data | 数据目录 |
| STATIC_DIR | ./static | 前端目录 |
| AListURL | - | AList 地址 |
| AListToken | - | AList token |
| LXServerURL | http://127.0.0.1:9527 | LX 服务 |
| WEB_USER | 登录用户名 |
| WEB_PASS |  | 登录密码 |
