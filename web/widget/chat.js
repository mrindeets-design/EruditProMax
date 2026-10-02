(function () {

    "use strict";


    // =========================================================
    // НАСТРОЙКИ
    // =========================================================

    const PENDING_KEY =
        "erudit-pending-question";

    const SESSION_KEY =
        "erudit-session-id";

    // Относительный URL для размещения на одном домене
    // Можно переопределить через window.ERUDIT_API_URL для раздельного размещения
    const BACKEND_URL =
        window.ERUDIT_API_URL || "";


    // =========================================================
    // DOM
    // =========================================================

    const messages =
        document.getElementById("messages");

    const form =
        document.getElementById("chatForm");

    const input =
        document.getElementById("chatInput");


    console.log(
        "✅ chat.js ЗАПУСТИЛСЯ"
    );


    // =========================================================
    // ПРОВЕРКА
    // =========================================================

    if (!messages) {

        console.error(
            "❌ НЕТ #messages"
        );

        return;
    }


    if (!form) {

        console.error(
            "❌ НЕТ #chatForm"
        );

        return;
    }


    if (!input) {

        console.error(
            "❌ НЕТ #chatInput"
        );

        return;
    }


    // =========================================================
    // ДОБАВИТЬ СООБЩЕНИЕ
    // =========================================================

    function addMessage(
        text,
        type,
        sources
    ) {

        const message =
            document.createElement("div");

        message.className =
            "message " + type;

        message.textContent =
            text;

        messages.appendChild(
            message
        );

        // Добавляем источники, если есть
        if (sources && sources.length > 0) {
            const sourcesDiv = document.createElement("div");
            sourcesDiv.className = "message-sources";
            sourcesDiv.style.marginTop = "8px";
            sourcesDiv.style.fontSize = "12px";
            sourcesDiv.style.opacity = "0.8";

            const sourcesTitle = document.createElement("div");
            sourcesTitle.textContent = "Источники:";
            sourcesTitle.style.fontWeight = "600";
            sourcesTitle.style.marginBottom = "4px";
            sourcesDiv.appendChild(sourcesTitle);

            sources.forEach((source) => {
                const link = document.createElement("a");
                link.href = source.url || "#";
                link.textContent = source.title || source.url || "Источник";
                link.target = "_blank";
                link.rel = "noopener noreferrer";
                link.style.display = "block";
                link.style.color = "#06468e";
                link.style.textDecoration = "none";
                link.style.marginTop = "2px";
                link.style.wordBreak = "break-word";
                link.addEventListener("mouseenter", function() {
                    this.style.textDecoration = "underline";
                });
                link.addEventListener("mouseleave", function() {
                    this.style.textDecoration = "none";
                });
                sourcesDiv.appendChild(link);
            });

            messages.appendChild(sourcesDiv);
        }

        messages.scrollTop =
            messages.scrollHeight;


        return message;
    }


    // =========================================================
    // ЭРУДИТ ПЕЧАТАЕТ
    // =========================================================

    function showTyping() {

        const old =
            document.getElementById(
                "erudit-typing"
            );

        if (old) {
            return;
        }


        const typing =
            document.createElement("div");

        typing.id =
            "erudit-typing";

        typing.className =
            "message bot";

        typing.textContent =
            "Эрудит печатает...";


        messages.appendChild(
            typing
        );


        messages.scrollTop =
            messages.scrollHeight;
    }


    function hideTyping() {

        const typing =
            document.getElementById(
                "erudit-typing"
            );

        if (typing) {
            typing.remove();
        }
    }


    // =========================================================
    // BACKEND
    // =========================================================

    async function askBackend(
        question
    ) {

        console.log(
            "📤 Отправляем backend:",
            question
        );

        // Получаем сохранённый session_id
        const sessionId = sessionStorage.getItem(SESSION_KEY);

        const body = {
            message: question
        };

        // Добавляем session_id если есть
        if (sessionId) {
            body.session_id = sessionId;
            console.log("📦 Используем session_id:", sessionId);
        }

        const response =
            await fetch(
                BACKEND_URL + "/api/chat",
                {
                    method: "POST",

                    headers: {
                        "Content-Type":
                            "application/json"
                    },

                    body: JSON.stringify(
                        body
                    )
                }
            );


        console.log(
            "📥 Backend HTTP:",
            response.status
        );


        if (!response.ok) {

            throw new Error(
                "HTTP " +
                response.status
            );
        }


        const data =
            await response.json();


        console.log("📥 Ответ backend:", data);
        console.log("📥 JSON backend:", JSON.stringify(data, null, 2));

        // Сохраняем или обновляем session_id
        if (data.session_id) {
            sessionStorage.setItem(SESSION_KEY, data.session_id);
            console.log("💾 Сохранён session_id:", data.session_id);
        }

        if (
            !data ||
            typeof data.reply !== "string"
        ) {

            throw new Error(
                "Backend не вернул reply"
            );
        }


        return {
            reply: data.reply,
            sources: data.sources || []
        };
    }


    // =========================================================
    // ОБРАБОТКА ВОПРОСА
    // =========================================================

    async function processQuestion(
        question
    ) {

        /*
         * САМОЕ ВАЖНОЕ:
         * сначала рисуем вопрос,
         * потом вообще трогаем backend.
         */

        addMessage(
            question,
            "user"
        );


        showTyping();

        // Блокируем повторную отправку
        input.disabled = true;
        form.querySelector("button").disabled = true;

        try {

            const response =
                await askBackend(
                    question
                );


            hideTyping();


            addMessage(
                response.reply,
                "bot",
                response.sources
            );


        } catch (error) {

            console.error(
                "❌ Ошибка backend:",
                error
            );


            hideTyping();


            addMessage(
                "Я получил ваш вопрос, но сейчас не удалось получить ответ от сервера.",
                "bot"
            );
        } finally {
            // Восстанавливаем управление
            input.disabled = false;
            form.querySelector("button").disabled = false;
            input.focus();
        }
    }


    // =========================================================
    // ВОПРОС ИЗ WELCOME
    // =========================================================

    const pendingQuestion =
        sessionStorage.getItem(
            PENDING_KEY
        );


    console.log(
        "📦 Вопрос из sessionStorage:",
        pendingQuestion
    );


    if (pendingQuestion) {

        /*
         * УДАЛЯЕМ ПОСЛЕ ТОГО,
         * как получили значение.
         */

        sessionStorage.removeItem(
            PENDING_KEY
        );


        /*
         * СРАЗУ показываем вопрос.
         */

        processQuestion(
            pendingQuestion
        );

    } else {

        addMessage(
            "Здравствуйте! Я Эрудит. Чем могу помочь?",
            "bot"
        );
    }


    // =========================================================
    // НОВЫЙ ВОПРОС В ЧАТЕ
    // =========================================================

    form.addEventListener(
        "submit",
        function (event) {

            event.preventDefault();


            const question =
                input.value.trim();


            if (!question) {

                input.focus();

                return;
            }


            input.value = "";


            processQuestion(
                question
            );
        }
    );


    input.focus();


})();