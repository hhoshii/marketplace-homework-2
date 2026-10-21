# Проверка навыков

Проверено 20.10.2026 через реальный `skill-installer` и чтение установленных SKILL.md.

| Навык | Источник и статус | Применение |
| --- | --- | --- |
| architecture-patterns | [wshobson/agents](https://github.com/wshobson/agents/tree/main/plugins/backend-development/skills/architecture-patterns), установлен | Сохранены владельцы User/Commerce; упрощено развёртывание до одного PostgreSQL с двумя изолированными БД, без новых слоёв |
| api-design-principles | [wshobson/agents](https://github.com/wshobson/agents/tree/main/plugins/backend-development/skills/api-design-principles), установлен | REST-ресурсы, коды ошибок, пагинация, проверка схем |
| writing-clearly-and-concisely | [richyuh/claude-skills](https://github.com/richyuh/claude-skills/tree/main/writing-clearly-and-concisely), установлен | Короткий README, отдельные архитектурные пояснения и отчёт |
| security-best-practices | [openai/skills](https://github.com/openai/skills/tree/main/skills/.curated/security-best-practices), установлен | Go/net-http: JWT, bcrypt, лимиты тела/таймауты, маскирование, race/vulnerability checks |
| security-threat-model | openai/skills, установлен | Доступен для будущего отдельного threat modeling; в этой работе не заявлен как применённый |
| api-guide | Доступен локально до начала работы | Русские описания и объектные схемы; внутренние TVM/Taxi-требования неприменимы к этому JWT-заданию |
| karpathy-guidelines | Доступен локально до начала работы | Убраны повторные проверки gateway, необязательный User lookup, второй E2E-режим и устаревшая обвязка; обязательная бизнес-логика сохранена |

В официальном curated-каталоге не оказалось специализированных system design/API writing навыков; experimental-путь вернул not found. Подходящие сторонние навыки найдены отдельно и установлены штатным установщиком. Для постоянного обнаружения новых навыков среда обычно перечитывает каталог на следующем ходе; содержимое установленных инструкций прочитано и применено в текущей работе.
