
# Invalid URL Test Cases

Here are some invalid URLs using various Markdown link and image syntaxes:

- [Broken Protocol](htp://invalid-url.com)  
  *Reason: Misspelled protocol ("htp" instead of "http")*

- [No Domain](http://)  
  *Reason: Missing domain*

- [Missing Name Before TLD](http://.com)  
  *Reason: Missing domain name before TLD*

- [Underscore in Domain](http://invalid_domain)  
  *Reason: Underscore in domain, not allowed in DNS hostnames*

- [Domain Starts with Hyphen](http://-example.com)  
  *Reason: Domain cannot start with a hyphen*

- [Double Dot in Domain](http://example..com)  
  *Reason: Double dot in domain*

- [Non-numeric Port](http://example.com:abc)  
  *Reason: Invalid port (non-numeric)*

- [Unsupported Protocol](ftp://example.com)  
  *Reason: Unsupported protocol (should be http/https)*

- [Space in Domain](http://example .com)  
  *Reason: Space in domain*

- [Extra Slash in Protocol](http:///example.com)  
  *Reason: Extra slash in protocol separator*

- ![Broken Image Link](http://)  
  *Reason: Image with missing domain*

- ![Invalid Protocol Image](htp://invalid-url.com/image.png)  
  *Reason: Image with misspelled protocol*

- ![Double Dot Image](http://example..com/pic.jpg)  
  *Reason: Image with double dot in domain*

- [![Image with Bad Link](http://)](htp://invalid-url.com)  
  *Reason: Image and link both with invalid URLs*

---

# Correctly Formatted but Nonexistent URLs

These URLs are syntactically correct but do not point to real sites:

- [Nonexistent Domain](https://this-domain-does-not-exist-123456789.com)

- [Fake Subdomain](https://foo.bar.baz.nonexistent-tld)

- [Unused TLD](https://example.madeuptld)

- [Long Random String](https://abcdefg1234567890.example.com)

- [Fake Image](https://notarealwebsite.com/image.png)

- ![Nonexistent Image](https://this-image-does-not-exist.com/pic.jpg)

- [![Fake Image Link](https://notarealwebsite.com/fake.png)](https://notarealwebsite.com/page)

- [Unregistered Domain](https://unregistered-website-xyz.com)

- [Fake Path](https://example.com/this/path/does/not/exist)

- [Nonexistent Page](https://example.com/404notfound)

---

# Valid URLs

These URLs are well-formed and point to known good sites:

- [Example Domain](https://example.com)

- [Wikipedia](https://en.wikipedia.org/wiki/Main_Page)

- [GitHub](https://github.com)

- [Google](https://www.google.com)

- [Mozilla Developer Network](https://developer.mozilla.org)

- [Go Documentation](https://go.dev/doc/)

- ![Valid Image](https://upload.wikimedia.org/wikipedia/commons/4/47/PNG_transparency_demonstration_1.png)

- [![GitHub Logo](https://github.githubassets.com/images/modules/logos_page/GitHub-Mark.png)](https://github.com)

- [Svelte](https://svelte.dev)

- [OpenAI](https://openai.com)




