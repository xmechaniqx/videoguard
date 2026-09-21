// VideoGuard — Основной JavaScript
// Лёгкий, без фреймворков

document.addEventListener('DOMContentLoaded', function() {
    console.log('VideoGuard инициализирован');
    
    // Обновлять статус камеры каждые 30 секунд
    if (document.querySelector('.camera-status')) {
        setInterval(updateCameraStatus, 30000);
    }
});

// Обновить статус камер через API
function updateCameraStatus() {
    fetch('/api/v1/cameras')
        .then(response => response.json())
        .then(cameras => {
            const cards = document.querySelectorAll('.camera-card');
            cameras.forEach((cam, index) => {
                if (cards[index]) {
                    const status = cards[index].querySelector('.camera-status');
                    if (status) {
                        status.className = 'camera-status status-online';
                    }
                }
            });
        })
        .catch(error => {
            console.error('Ошибка обновления статуса:', error);
        });
}

// Форматирование даты в русском формате
function formatDate(dateString) {
    const date = new Date(dateString);
    return date.toLocaleString('ru-RU', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit'
    });
}

// Форматирование размера файла
function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}
