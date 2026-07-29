import React from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Route, Routes } from 'react-router-dom';
import Home from './home.jsx';
import DocsLayout from './docs.jsx';

createRoot(document.getElementById('root')).render(
  <BrowserRouter>
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/docs/*" element={<DocsLayout />} />
    </Routes>
  </BrowserRouter>,
);
