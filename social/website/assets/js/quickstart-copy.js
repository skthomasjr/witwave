const codeWindows = document.querySelectorAll('[data-code-window]');

codeWindows.forEach((windowEl) => {
  const button = windowEl.querySelector('.copy-code-button');
  const code = windowEl.querySelector('code');

  if (!button || !code) {
    return;
  }

  button.setAttribute('aria-label', 'Copy command');
  button.setAttribute('title', 'Copy command');
  button.addEventListener('click', async () => {
    const command = code.textContent.trimEnd();

    try {
      await copyText(command);
      setButtonState(button, 'Copied', 'copied', 'Command copied');
    } catch (error) {
      console.warn('Unable to copy command', error);
      setButtonState(button, 'Copy failed', 'copy-failed', 'Copy failed');
    }
  });
});

async function copyText(text) {
  let clipboardError;

  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch (error) {
      clipboardError = error;
    }
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.inset = '0 auto auto 0';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.focus({ preventScroll: true });
  textarea.select();
  textarea.setSelectionRange(0, textarea.value.length);

  try {
    const copied = document.execCommand('copy');
    if (!copied) {
      throw new Error('document.execCommand("copy") returned false');
    }
  } catch (error) {
    throw clipboardError || error;
  } finally {
    textarea.remove();
  }
}

function setButtonState(button, label, className, ariaLabel) {
  const originalLabel = button.dataset.originalLabel || button.textContent;
  const originalAriaLabel = button.dataset.originalAriaLabel || button.getAttribute('aria-label') || originalLabel;
  button.dataset.originalLabel = originalLabel;
  button.dataset.originalAriaLabel = originalAriaLabel;
  button.textContent = label;
  button.setAttribute('aria-label', ariaLabel);
  button.setAttribute('title', ariaLabel);
  button.classList.remove('copied', 'copy-failed');
  button.classList.add(className);

  window.setTimeout(() => {
    button.textContent = originalLabel;
    button.setAttribute('aria-label', originalAriaLabel);
    button.setAttribute('title', originalAriaLabel);
    button.classList.remove(className);
  }, 1600);
}
