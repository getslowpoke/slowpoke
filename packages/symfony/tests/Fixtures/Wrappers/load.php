<?php

// The Messenger wrappers MessageName unwraps, with the public shape of the real classes, for the
// Symfony versions of the matrix that do not ship them (5.4 has none; process and scheduler are not
// installed at all). Where the real class exists it is the one used.

namespace Symfony\Component\Console\Messenger;

if (!class_exists(RunCommandMessage::class)) {
    class RunCommandMessage
    {
        /** @var string */
        public $input;
        public function __construct(string $input) { $this->input = $input; }
    }
}

namespace Symfony\Component\Messenger\Message;

if (!class_exists(RedispatchMessage::class)) {
    final class RedispatchMessage
    {
        /** @var object */
        public $envelope;
        /** @var string[]|string */
        public $transportNames;
        /** @param string[]|string $transportNames */
        public function __construct(object $envelope, $transportNames = []) { $this->envelope = $envelope; $this->transportNames = $transportNames; }
    }
}

namespace Symfony\Component\Process\Messenger;

if (!class_exists(RunProcessMessage::class)) {
    class RunProcessMessage
    {
        /** @var string[] */
        public $command;
        /** @var string|null */
        public $commandLine;
        /** @param string[] $command */
        public function __construct(array $command) { $this->command = $command; }
        public static function fromShellCommandline(string $command): self
        {
            $message = new self([]);
            $message->commandLine = $command;
            return $message;
        }
    }
}

namespace Symfony\Component\Scheduler\Messenger;

if (!class_exists(ServiceCallMessage::class)) {
    class ServiceCallMessage
    {
        /** @var string */
        private $serviceId;
        /** @var string */
        private $method;
        /** @var array<mixed> */
        private $arguments;
        /** @param array<mixed> $arguments */
        public function __construct(string $serviceId, string $method = '__invoke', array $arguments = [])
        {
            $this->serviceId = $serviceId;
            $this->method = $method;
            $this->arguments = $arguments;
        }
        public function getServiceId(): string { return $this->serviceId; }
        public function getMethod(): string { return $this->method; }
        /** @return array<mixed> */
        public function getArguments(): array { return $this->arguments; }
    }
}
