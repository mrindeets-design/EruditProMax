(function () {
    "use strict";

    const form = document.getElementById("questionForm");
    const input = document.getElementById("questionInput");

    if (!form || !input) {
        console.error("❌ Не найдены элементы приветственной страницы");
        return;
    }

    form.addEventListener("submit", function (event) {
        event.preventDefault();

        const question = input.value.trim();

        if (!question) {
            input.focus();
            return;
        }

        // Сохраняем вопрос перед переходом
        sessionStorage.setItem(
            "erudit-pending-question",
            question
        );

        // Открываем чат
        window.location.href = "/widget/chat.html";
    });

    input.focus();

    console.log("✅ Welcome page готова");
})();