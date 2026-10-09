# Overdue invoices # not a heading

- Site: prod
- Model: p1 / claude-sonnet-5-5
- Profile: Explore
- Started: 2026-10-09 10:00 UTC

## You

Which invoices are overdue?

*[image: image/png]*

## Assistant

I will look.

<details>
<summary>list_docs on prod (ok)</summary>

Arguments:

```json
{
  "doctype": "Sales Invoice",
  "filters": {
    "status": "Overdue"
  }
}
```

Result:

````text
[{"name":"SINV-1"}]
```
not a fence end

... (3 more characters)
````

</details>

<details>
<summary>get_doc on prod (error, declined)</summary>

Arguments:

```json
{
  "doctype": "Sales Invoice",
  "name": "<b>SINV-1</b>"
}
```

Error:

```text
xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
... (20 more characters)
```

</details>

## Assistant

SINV-1 is overdue.
