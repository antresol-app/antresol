---
title: Документация
---

# Документация

Короткие инструкции по задачам. Начните с [установки](install.html).

{% for section in site.data.guide.ru %}
## {{ section.title }}
{% for p in section.pages %}
- [{{ p.title }}]({{ p.url }})
{%- endfor %}
{% endfor %}

## Для разработчиков

- [Как добавить язык](translating.html)
- [Контракт расширений](../addons/contract.html) (на английском)
- [Теги сборки](../build-tags.html) (на английском)
