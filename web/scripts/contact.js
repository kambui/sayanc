(function () {
  'use strict';
  const form = document.getElementById('contact-form');
  if (!form) return;
  const button = document.getElementById('contact-send');
  const status = document.getElementById('contact-status');
  let widget, loading = false, busy = false, previous = '', requestId = '', requestTime = 0;
  const say = (message) => { status.textContent = message; };

  async function setup() {
    if (loading || widget !== undefined) return;
    loading = true;
    say('Loading spam protection…');
    try {
      const response = await fetch('/api/contact/config', { cache: 'no-store' });
      const config = await response.json();
      if (!response.ok) throw new Error(config.message);
      await new Promise((resolve, reject) => {
        const script = document.createElement('script');
        window.contactCaptchaReady = resolve;
        script.src = 'https://www.google.com/recaptcha/enterprise.js?onload=contactCaptchaReady&render=explicit';
        script.async = true;
        script.onerror = reject;
        document.head.appendChild(script);
      });
      widget = grecaptcha.enterprise.render('contact-captcha', {
        sitekey: config.siteKey,
        size: window.innerWidth < 400 ? 'compact' : 'normal',
        callback: () => { if (!busy) button.disabled = false; say(''); },
        'expired-callback': () => { button.disabled = true; say('Please complete a fresh spam check.'); },
        'error-callback': () => { button.disabled = true; say('Spam protection is unavailable. Please email me directly.'); }
      });
      say('Complete the spam check before sending.');
    } catch (error) {
      say('The form is temporarily unavailable. Please email me directly.');
      loading = false;
    }
  }
  form.addEventListener('focusin', setup);
  if ('IntersectionObserver' in window) {
    const observer = new IntersectionObserver((entries) => {
      if (entries.some(entry => entry.isIntersecting)) { observer.disconnect(); setup(); }
    });
    observer.observe(form);
  } else setup();

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (busy || widget === undefined || !form.reportValidity()) return;
    const captcha = grecaptcha.enterprise.getResponse(widget);
    if (!captcha) { say('Please complete the spam check.'); return; }
    const details = {
      name: form.elements.name.value.trim(), email: form.elements.email.value.trim(),
      message: form.elements.message.value.trim(), website: form.elements.website.value
    };
    const fingerprint = JSON.stringify(details);
    // Keep a stable id across uncertain network failures; never store message text on disk.
    if (fingerprint !== previous || Date.now() - requestTime > 23 * 60 * 60 * 1000) {
      requestId = crypto.randomUUID(); previous = fingerprint; requestTime = Date.now();
    }
    busy = true; button.disabled = true; button.textContent = 'Sending…';
    say('Sending your message…');
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 35000);
    try {
      const response = await fetch('/api/contact', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...details, captcha, requestId }), signal: controller.signal
      });
      const result = await response.json();
      say(result.message || 'Unable to confirm delivery. Please try again or email me directly.');
      if (response.ok) { form.reset(); previous = ''; }
    } catch (error) {
      say('Delivery could not be confirmed. Your text is still in the form; retry with the same details or email me directly.');
    } finally {
      clearTimeout(timer); busy = false; button.textContent = 'Send'; button.disabled = true;
      grecaptcha.enterprise.reset(widget);
    }
  });
}());
