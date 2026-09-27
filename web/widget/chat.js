(function () {

    "use strict";


    // =========================================================
    // НАСТРОЙКИ
    // =========================================================

    const PENDING_KEY =
        "erudit-pending-question";

    const BACKEND_URL =
        "http://localhost:3000";


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
        type
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


        const response =
            await fetch(
                BACKEND_URL + "/api/chat",
                {
                    method: "POST",

                    headers: {
                        "Content-Type":
                            "application/json"
                    },

                    body: JSON.stringify({
                        message:
                        question
                    })
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


        if (
            !data ||
            typeof data.reply !== "string"
        ) {

            throw new Error(
                "Backend не вернул reply"
            );
        }


        return data.reply;
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


        try {

            const reply =
                await askBackend(
                    question
                );


            hideTyping();


            addMessage(
                reply,
                "bot"
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