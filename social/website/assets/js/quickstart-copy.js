const codeWindows = document.querySelectorAll('[data-code-window]');

codeWindows.forEach((windowEl) => {
  const button = windowEl.querySelector('.copy-code-button');
  const code = windowEl.querySelector('code');

  if (!button || !code) {
    return;
  }

  button.setAttribute('aria-label', 'Copy command');
  button.addEventListener('click', async () => {
    const command = code.textContent.trimEnd();

    try {
      await copyText(command);
      setButtonState(button, 'Copied', 'copied');
    } catch (error) {
      console.warn('Unable to copy command', error);
      setButtonState(button, 'Copy failed', 'copy-failed');
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

function setButtonState(button, label, className) {
  const originalLabel = button.dataset.originalLabel || button.textContent;
  button.dataset.originalLabel = originalLabel;
  button.textContent = label;
  button.classList.remove('copied', 'copy-failed');
  button.classList.add(className);

  window.setTimeout(() => {
    button.textContent = originalLabel;
    button.classList.remove(className);
  }, 1600);
}
