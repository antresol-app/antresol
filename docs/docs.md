---
title: Documentation
---

# Documentation

Short, task-based guides. Start with [Install](install.html).

{% for section in site.data.guide.en %}
## {{ section.title }}
{% for p in section.pages %}
- [{{ p.title }}]({{ p.url }})
{%- endfor %}
{% endfor %}

## For developers

- [Translating Antresol](translating.html)
- [The addon contract](addons/contract.html)
- [Build tags](build-tags.html)
