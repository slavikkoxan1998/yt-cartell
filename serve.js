import http from 'http';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const DOCS_DIR = path.join(__dirname, 'docs', 'website');
const PORT = process.env.PORT || 5195;

const MIME_TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.mp4': 'video/mp4',
  '.webm': 'video/webm',
  '.vtt': 'text/vtt; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8'
};

const server = http.createServer((req, res) => {
  // Security headers
  res.setHeader('X-Content-Type-Options', 'nosniff');
  
  const origin = req.headers.origin;
  const ALLOWED_ORIGINS = [
    'https://yt-cartel.n8n-accaisona.site',
    'https://yt-cartell.n8n-accaisona.site',
    'http://localhost:5195',
    'http://127.0.0.1:5195'
  ];
  if (origin && ALLOWED_ORIGINS.includes(origin)) {
    res.setHeader('Access-Control-Allow-Origin', origin);
  }

  let parsedUrl;
  try {
    parsedUrl = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  } catch (e) {
    res.writeHead(400, { 'Content-Type': 'text/plain' });
    res.end('Bad Request');
    return;
  }

  let pathname = decodeURIComponent(parsedUrl.pathname);
  let safePath = path.normalize(pathname).replace(/^(\.\.[\/\\])+/, '');
  let filePath = path.join(DOCS_DIR, safePath);

  // Check path traversal
  if (!filePath.startsWith(DOCS_DIR)) {
    res.writeHead(403, { 'Content-Type': 'text/plain' });
    res.end('Forbidden');
    return;
  }

  // Handle directory requests
  if (fs.existsSync(filePath) && fs.statSync(filePath).isDirectory()) {
    filePath = path.join(filePath, 'index.html');
  } else if (!fs.existsSync(filePath) && fs.existsSync(filePath + '.html')) {
    filePath = filePath + '.html';
  }

  if (!fs.existsSync(filePath) || fs.statSync(filePath).isDirectory()) {
    // 404 fallback
    res.writeHead(404, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end('<h1>404 Not Found</h1><p>The requested page was not found on YT Cartell Studio server.</p>');
    return;
  }

  const stat = fs.statSync(filePath);
  const totalSize = stat.size;
  const ext = path.extname(filePath).toLowerCase();
  const contentType = MIME_TYPES[ext] || 'application/octet-stream';

  const range = req.headers.range;

  if (range && range.startsWith('bytes=')) {
    const rangeVal = range.slice(6);
    const [startStr, endStr] = rangeVal.split('-');

    let start = 0;
    let end = totalSize - 1;

    if (startStr === '' && endStr !== '') {
      // e.g. bytes=-500
      start = totalSize - parseInt(endStr, 10);
    } else {
      start = parseInt(startStr, 10);
      if (endStr) {
        end = parseInt(endStr, 10);
      }
    }

    if (isNaN(start) || isNaN(end) || start < 0 || start >= totalSize || end < start || end >= totalSize) {
      res.writeHead(416, {
        'Content-Range': `bytes */${totalSize}`,
        'Content-Type': 'text/plain'
      });
      res.end('Requested range not satisfiable');
      return;
    }

    const chunksize = (end - start) + 1;
    const file = fs.createReadStream(filePath, { start, end });

    res.writeHead(206, {
      'Content-Range': `bytes ${start}-${end}/${totalSize}`,
      'Accept-Ranges': 'bytes',
      'Content-Length': chunksize,
      'Content-Type': contentType,
      'Cache-Control': ext === '.html' ? 'no-cache' : 'public, max-age=86400'
    });

    file.pipe(res);
  } else {
    res.writeHead(200, {
      'Content-Length': totalSize,
      'Accept-Ranges': 'bytes',
      'Content-Type': contentType,
      'Cache-Control': ext === '.html' ? 'no-cache' : 'public, max-age=86400'
    });

    fs.createReadStream(filePath).pipe(res);
  }
});

server.listen(PORT, '0.0.0.0', () => {
  console.log(`YT Cartell Studio web server running at http://0.0.0.0:${PORT}`);
});
