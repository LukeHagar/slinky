# Emphasis and punctuation edge cases

This line has an emphasized URL: *https://sailpoint.api.identitynow.com/v2024*

This one has trailing punctuation: https://example.com/path), https://example.com/foo,

Balanced parentheses should remain: https://example.com/q?(x)

Inline code should be ignored: `https://ignore.me/inside/code`

Fenced code should be ignored:

```
curl https://ignore.me/in/fenced/code
```


