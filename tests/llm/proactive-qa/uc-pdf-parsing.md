# Use Case: PDF File Parsing

**Actor**: Developer or AI agent reading a PDF file via the tzro engine's `read_file` tool.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`)
**Backend**: Internal tool dispatch (no HTTP endpoint — triggered via `read_file` tool)
**Priority**: P1

---

## Intent

A user wants to extract text and visual content from a PDF document using tzro's local tools, without sending any data to external services. The engine should parse native text, extract embedded images, and perform OCR using the best available local backend (vision model or tesseract), returning the combined content.

## Preconditions

- App is running with the engine configured
- A PDF file exists on disk within allowed paths
- For vision OCR: mmproj companion model is downloaded alongside the base model
- For tesseract OCR: `tesseract` CLI is installed on the system

## Success Criteria

- [ ] User can read a PDF file by passing its path to the `read_file` tool
- [ ] Native digital text is extracted correctly from text-based PDFs
- [ ] Embedded images are extracted and OCR is attempted
- [ ] OCR uses the local vision model when mmproj is available
- [ ] OCR falls back to tesseract when vision model is unavailable
- [ ] OCR is skipped gracefully when neither backend is available
- [ ] Output supports line-range selection (startLine / endLine) like regular files
- [ ] Large PDF output is truncated at 200 lines with a hint to paginate
- [ ] Temporary image files are cleaned up after processing
- [ ] Error messages are user-friendly when the file doesn't exist or can't be parsed

## Edge Cases to Probe

- PDF with no native text (image-only scans)
- PDF with no images (pure digital text)
- Corrupted or malformed PDF files
- Very large PDFs (100+ pages) — performance and memory behavior
- PDF files outside the allowed path restrictions
- Password-protected PDFs

## Anti-Patterns to Watch For

- [ ] Raw stack traces or panics surfaced to the user
- [ ] Temporary image files left behind in the cache directory
- [ ] Sensitive PDF content leaking to cloud APIs when `strict-local` privacy is set
- [ ] Silent failure where no text is returned and no error is shown
- [ ] Blocking the entire engine while parsing a large PDF
